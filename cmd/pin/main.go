// Package main: grove-apps catalog digest pinner.
//
// Replaces the placeholder sha256:0000... image_digest values in
// v0.3 manifests with real digests fetched from the registry.
//
// Usage:
//
//	go run ./cmd/pin              # pin every manifest under apps/
//	go run ./cmd/pin apps/usememos/memos/manifest.json  # one file
//	go run ./cmd/pin --dry-run    # show what would change, don't write
//
// Reads source.image, source.upstream_image (mirror type), and any
// services[i].image entries with placeholder digests, HEADs each
// against its registry, captures the Docker-Content-Digest header,
// and writes back the manifest with the real digest substituted.
//
// Anonymous bearer-token flow only — public images on docker.io
// and ghcr.io. Private mirrors / build sources need a separate
// authenticated path that's not wired here.
//
// Run periodically (or as part of release cut) to keep digests
// current. Re-pinning a manifest that already has a real digest
// is a no-op — the placeholder check skips non-placeholder values.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/solcreek/grove-apps/internal/registry"
)

// placeholder is the sentinel digest value used during v0.2→v0.3
// migration. Any image_digest matching this is treated as
// unpinned and rewritten with the real digest.
const placeholder = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func main() {
	dryRun := flag.Bool("dry-run", false, "show what would change, don't write files")
	flag.Parse()

	paths := flag.Args()
	if len(paths) == 0 {
		// Walk every manifest under apps/.
		err := filepath.Walk("apps", func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || filepath.Base(p) != "manifest.json" {
				return nil
			}
			paths = append(paths, p)
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "walk apps/: %v\n", err)
			os.Exit(2)
		}
	}
	sort.Strings(paths)

	client := &http.Client{Timeout: 30 * time.Second}

	// Fetch sequentially per file but parallel across files — registries
	// rate-limit per-source-IP, so going wide across many manifests
	// hits the limit fast. Cap parallelism modestly.
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var anyErr bool
	var errMu sync.Mutex

	for _, p := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := pinFile(path, client, *dryRun); err != nil {
				errMu.Lock()
				anyErr = true
				errMu.Unlock()
				fmt.Fprintf(os.Stderr, "✗ %s: %v\n", path, err)
			}
		}(p)
	}
	wg.Wait()

	if anyErr {
		os.Exit(1)
	}
}

// pinFile rewrites placeholder digests in path. Returns nil + a
// "no placeholders" message when nothing needed pinning, so the
// command is idempotent.
func pinFile(path string, client *http.Client, dryRun bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}

	// Inventory of (image, digest-field-name) pairs that need filling.
	// Order MUST match the order placeholders appear in the source
	// text — we use that ordering for in-place text replacement.
	type todo struct {
		image string
		field string
	}
	var inventory []todo

	if src, _ := doc["source"].(map[string]any); src != nil {
		// source.image_digest (always, for upstream + mirror + build)
		if d, _ := src["image_digest"].(string); d == placeholder {
			img, _ := src["image"].(string)
			if img == "" {
				return fmt.Errorf("source.image is empty but image_digest is placeholder")
			}
			inventory = append(inventory, todo{image: img, field: "source.image_digest"})
		}
		// source.upstream_digest (mirror only — points at the
		// pre-mirror upstream image)
		if d, _ := src["upstream_digest"].(string); d == placeholder {
			ui, _ := src["upstream_image"].(string)
			if ui == "" {
				return fmt.Errorf("source.upstream_image is empty but upstream_digest is placeholder")
			}
			inventory = append(inventory, todo{image: ui, field: "source.upstream_digest"})
		}
	}

	if services, _ := doc["services"].([]any); services != nil {
		for i, sRaw := range services {
			s, _ := sRaw.(map[string]any)
			if s == nil {
				continue
			}
			if d, _ := s["image_digest"].(string); d == placeholder {
				img, _ := s["image"].(string)
				if img == "" {
					return fmt.Errorf("services[%d].image is empty but image_digest is placeholder", i)
				}
				inventory = append(inventory, todo{image: img, field: fmt.Sprintf("services[%d].image_digest", i)})
			}
		}
	}

	if len(inventory) == 0 {
		fmt.Printf("  no placeholders: %s\n", path)
		return nil
	}

	// Fetch each digest. Bail on first error — keeping partial
	// writes out of the catalog is worth losing progress.
	digests := make([]string, len(inventory))
	for i, item := range inventory {
		d, err := registry.FetchDigest(client, item.image)
		if err != nil {
			return fmt.Errorf("%s (%s): %w", item.field, item.image, err)
		}
		digests[i] = d
		fmt.Printf("  %s\n    %s\n    %s → %s\n", path, item.field, item.image, d)
	}

	// In-place text replacement. Each placeholder occurrence in
	// document text gets the next digest from our inventory, in
	// order. Document order matches inventory order for the
	// manifests we ship (source above services[]). Bail if counts
	// disagree — better to crash than write a half-edited file.
	placeholderBytes := []byte(placeholder)
	if want, got := len(inventory), bytes.Count(raw, placeholderBytes); want != got {
		return fmt.Errorf("placeholder count mismatch: inventory has %d, text has %d (manifest order may differ from canonical)", want, got)
	}

	out := raw
	for _, d := range digests {
		out = bytes.Replace(out, placeholderBytes, []byte(d), 1)
	}

	if dryRun {
		fmt.Printf("  (dry-run: %s not written)\n", path)
		return nil
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	fmt.Printf("  ✓ %s pinned (%d digest%s)\n", path, len(inventory), pluralS(len(inventory)))
	return nil
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// init quiet: avoid pulling fmt.Println into stderr by mistake in
// short-form output above; the package only depends on fmt + os.
var _ = strings.Builder{}

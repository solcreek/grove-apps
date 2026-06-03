// Package main: catalog validator.
//
// Two layers of catalog QA, both fast and PR-friendly:
//
//	Layer 1 — structural / referential checks on every manifest
//	          and registry.json. Catches typos, missing fields,
//	          slug↔path mismatches, dangling aliases.
//	Layer 2 — image availability. HEAD requests against the
//	          container registry for each manifest's source.image.
//	          Catches "we pinned a tag the upstream maintainer
//	          deleted" and arch-mismatch issues.
//
// Layer 3 (actually running each container + probing the HTTP
// endpoint) lives in the GitHub Actions workflow because it needs
// docker, not just a JSON parser.
//
// Usage:
//
//	go run ./cmd/validate              # validate everything
//	go run ./cmd/validate apps/...     # validate a subset
//	go run ./cmd/validate --no-network # skip layer 2 (offline)
//
// Exit codes:
//
//	0  all good
//	1  one or more validation errors
//	2  usage error (missing files etc)
package main

import (
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
)

// Manifest mirrors the v0.2 catalog schema. Unknown fields pass
// through (we use map[string]any for sub-objects we don't need to
// validate deeply) so a schema bump doesn't immediately break the
// validator. Required fields per SCHEMA.md.
type Manifest struct {
	SchemaVersion string         `json:"schema_version"`
	Slug          string         `json:"slug"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	Version       string         `json:"version"`
	Description   string         `json:"description"`
	Upstream      string         `json:"upstream"`
	License       string         `json:"license"`
	Tags          []string       `json:"tags"`
	Image         string         `json:"image"`
	Arch          []string       `json:"arch"`
	Ports         []Port         `json:"ports"`
	Source        map[string]any `json:"source"`
	Display       map[string]any `json:"display"`
	SmokeTest     *SmokeTest     `json:"smoke_test,omitempty"`
}

// SmokeTest carries hints that apply ONLY during Layer 3 smoke
// validation in CI — not to grove install. Used by apps with
// boot-time safety checks the smoke environment can't naturally
// satisfy.
//
//   ExtraEnv  Additional env vars injected before docker run.
//             E.g. vaultwarden's "I_REALLY_WANT_VOLATILE_STORAGE=true"
//             — grove install satisfies the persistent-volume guard
//             by actually mounting a volume, smoke can't.
//
//   Skip      Set true for apps the smoke harness fundamentally
//             cannot handle (e.g. multi-container compose, needs
//             an external DB). The validator surfaces "skipped"
//             so a long-disabled app stays visible.
type SmokeTest struct {
	ExtraEnv map[string]string `json:"extra_env,omitempty"`
	Skip     bool              `json:"skip,omitempty"`
}

type Port struct {
	Name      string `json:"name"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"`
}

type Registry struct {
	SchemaVersion string            `json:"schema_version"`
	Apps          []string          `json:"apps"`
	Aliases       map[string]string `json:"aliases"`
}

// validationError carries enough context for a CI log to surface
// the exact file and field that's wrong, so contributors don't
// have to bisect their PR.
type validationError struct {
	File  string
	Field string
	Msg   string
}

func (e validationError) String() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s: %s", e.File, e.Field, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Msg)
}

func main() {
	noNetwork := flag.Bool("no-network", false, "skip image-availability checks (Layer 2)")
	flag.Parse()

	root := "."
	var errs []validationError

	// Layer 1.a — every manifest under apps/<owner>/<name>/manifest.json
	manifests := map[string]*Manifest{} // keyed by slug
	manifestPaths := map[string]string{} // slug → file path
	err := filepath.Walk(filepath.Join(root, "apps"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || filepath.Base(path) != "manifest.json" {
			return nil
		}
		// Dispatch by schema_version: v0.3 takes the JSON Schema
		// path (in v3.go), v0.1/v0.2 stay on the legacy Go-struct
		// path. peekSchemaVersion is cheap (one ReadFile + minimal
		// json.Unmarshal of a single field), so we never duplicate
		// the parse on the common case.
		sv, peekErr := peekSchemaVersion(path)
		if peekErr != nil {
			errs = append(errs, validationError{File: path, Msg: "read: " + peekErr.Error()})
			return nil
		}
		if isV3(sv) {
			m3, v3Errs := validateV3(path, root)
			errs = append(errs, v3Errs...)
			if m3 == nil {
				return nil
			}
			shim := shimV3ToManifest(m3)
			manifests[shim.Slug] = shim
			manifestPaths[shim.Slug] = path
			return nil
		}
		m, fileErrs := loadManifest(path)
		errs = append(errs, fileErrs...)
		if m == nil {
			return nil
		}
		errs = append(errs, validateManifest(path, m, root)...)
		manifests[m.Slug] = m
		manifestPaths[m.Slug] = path
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk apps/: %v\n", err)
		os.Exit(2)
	}

	// Layer 1.b — registry.json structure + cross-refs.
	regPath := filepath.Join(root, "registry.json")
	if reg, regErrs := loadRegistry(regPath); reg != nil {
		errs = append(errs, regErrs...)
		errs = append(errs, validateRegistryCrossRefs(regPath, reg, manifests)...)
	} else {
		errs = append(errs, regErrs...)
	}

	// Layer 2 — image availability. Parallel HEADs because each can
	// take seconds; serial would be O(N×latency).
	if !*noNetwork {
		errs = append(errs, checkImageAvailability(manifests, manifestPaths)...)
	}

	if len(errs) == 0 {
		fmt.Fprintf(os.Stderr, "✓ validated %d manifests, all checks passed\n", len(manifests))
		return
	}

	sort.Slice(errs, func(i, j int) bool {
		if errs[i].File != errs[j].File {
			return errs[i].File < errs[j].File
		}
		return errs[i].Field < errs[j].Field
	})
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, e)
	}
	fmt.Fprintf(os.Stderr, "\n✗ %d validation error(s)\n", len(errs))
	os.Exit(1)
}

func loadManifest(path string) (*Manifest, []validationError) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, []validationError{{File: path, Msg: "read: " + err.Error()}}
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, []validationError{{File: path, Msg: "json: " + err.Error()}}
	}
	return &m, nil
}

func loadRegistry(path string) (*Registry, []validationError) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, []validationError{{File: path, Msg: "read: " + err.Error()}}
	}
	var r Registry
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, []validationError{{File: path, Msg: "json: " + err.Error()}}
	}
	return &r, nil
}

// validateManifest pins the load-bearing fields:
//   - schema_version present + recognized
//   - slug matches the file's owner/name path
//   - required user-facing fields non-empty
//   - kind is one of the known values
//   - ports declare valid container ports (1-65535)
//   - source.type is one of upstream/mirror/build
//   - source.image equals top-level image (consistency invariant)
func validateManifest(path string, m *Manifest, root string) []validationError {
	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	// Slug must equal the relative path under apps/.
	// e.g. apps/pocketbase/pocketbase/manifest.json → slug = pocketbase/pocketbase
	rel, _ := filepath.Rel(filepath.Join(root, "apps"), filepath.Dir(path))
	if rel != m.Slug {
		add("slug", fmt.Sprintf("path-vs-slug mismatch: path implies %q, slug is %q", rel, m.Slug))
	}

	if m.SchemaVersion == "" {
		add("schema_version", "required")
	} else if m.SchemaVersion != "0.1" && m.SchemaVersion != "0.2" {
		add("schema_version", fmt.Sprintf("unknown version %q (expected 0.1 or 0.2)", m.SchemaVersion))
	}
	if m.Name == "" {
		add("name", "required")
	}
	if m.Version == "" {
		add("version", "required")
	}
	if m.Description == "" {
		add("description", "required")
	}
	if m.Upstream == "" {
		add("upstream", "required")
	}
	if m.License == "" {
		add("license", "required")
	}
	if len(m.Tags) == 0 {
		add("tags", "at least one tag required")
	}

	switch m.Kind {
	case "image":
		// only kind we support today
	case "":
		add("kind", "required")
	default:
		add("kind", fmt.Sprintf("unsupported kind %q (only 'image' is supported in v0.2)", m.Kind))
	}

	if m.Image == "" {
		add("image", "required")
	}

	if len(m.Arch) == 0 {
		add("arch", "at least one of amd64/arm64 required")
	} else {
		for _, a := range m.Arch {
			if a != "amd64" && a != "arm64" {
				add("arch", fmt.Sprintf("unsupported arch %q", a))
			}
		}
	}

	if len(m.Ports) == 0 {
		add("ports", "at least one port required")
	} else {
		for i, p := range m.Ports {
			if p.Name == "" {
				add(fmt.Sprintf("ports[%d].name", i), "required")
			}
			if p.Container < 1 || p.Container > 65535 {
				add(fmt.Sprintf("ports[%d].container", i), fmt.Sprintf("out of range: %d", p.Container))
			}
			if p.Protocol != "tcp" && p.Protocol != "udp" && p.Protocol != "" {
				add(fmt.Sprintf("ports[%d].protocol", i), fmt.Sprintf("unknown %q (expected tcp or udp)", p.Protocol))
			}
		}
	}

	// Source provenance (v0.2 addition). Check shape consistency
	// when present; older manifests at schema 0.1 are allowed to
	// omit it for backward compat.
	if m.Source != nil {
		st, _ := m.Source["type"].(string)
		switch st {
		case "upstream", "mirror", "build":
		case "":
			add("source.type", "required when source is set")
		default:
			add("source.type", fmt.Sprintf("unknown type %q (upstream|mirror|build)", st))
		}
		if srcImg, _ := m.Source["image"].(string); srcImg != "" && srcImg != m.Image {
			add("source.image", fmt.Sprintf("must equal top-level image (%q vs %q)", srcImg, m.Image))
		}
	}

	if m.Display != nil {
		if cat, _ := m.Display["category"].(string); cat == "" {
			add("display.category", "required")
		}
	} else {
		add("display", "required")
	}

	return errs
}

// validateRegistryCrossRefs ensures:
//   - every slug in apps[] has a manifest dir
//   - every manifest has an entry in apps[]
//   - every alias value resolves to a registered slug
//   - aliases keys don't contain '/' (would be a full slug, not alias)
func validateRegistryCrossRefs(path string, reg *Registry, manifests map[string]*Manifest) []validationError {
	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	listed := map[string]bool{}
	for _, slug := range reg.Apps {
		listed[slug] = true
		if _, ok := manifests[slug]; !ok {
			add("apps", fmt.Sprintf("listed slug %q has no manifest", slug))
		}
	}
	for slug := range manifests {
		if !listed[slug] {
			add("apps", fmt.Sprintf("manifest %q is not listed in registry.apps", slug))
		}
	}
	for alias, target := range reg.Aliases {
		if strings.Contains(alias, "/") {
			add(fmt.Sprintf("aliases.%s", alias), "alias keys must not contain '/' (use full slug directly)")
		}
		if !listed[target] {
			add(fmt.Sprintf("aliases.%s", alias), fmt.Sprintf("points to %q which is not in apps[]", target))
		}
	}
	return errs
}

// checkImageAvailability HEADs each manifest's image at its
// registry endpoint. Parallelised because each round-trip is
// hundreds of ms; serial would compound to a slow CI step on a
// growing catalog.
func checkImageAvailability(manifests map[string]*Manifest, paths map[string]string) []validationError {
	type result struct {
		path string
		err  *validationError
	}
	results := make(chan result, len(manifests))
	var wg sync.WaitGroup

	client := &http.Client{Timeout: 10 * time.Second}

	for slug, m := range manifests {
		wg.Add(1)
		go func(slug string, m *Manifest) {
			defer wg.Done()
			path := paths[slug]
			if m.Image == "" {
				return
			}
			if err := headImage(client, m.Image); err != nil {
				results <- result{path: path, err: &validationError{
					File: path, Field: "image",
					Msg:  fmt.Sprintf("not reachable (%s): %v", m.Image, err),
				}}
			}
		}(slug, m)
	}

	wg.Wait()
	close(results)

	var errs []validationError
	for r := range results {
		if r.err != nil {
			errs = append(errs, *r.err)
		}
	}
	return errs
}

// headImage issues an OCI manifest HEAD for image ref. Handles
// docker.io, ghcr.io, and plain registries. Bearer-token flow for
// public images via the registry's WWW-Authenticate challenge.
//
// Returns nil when the image is reachable (200 / 401 with valid
// challenge that we then satisfy). Errors are user-friendly:
// "404 not found", "rate limited", etc.
func headImage(client *http.Client, ref string) error {
	registry, repo, tag := parseImageRef(ref)
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, repo, tag)

	req, _ := http.NewRequest("HEAD", manifestURL, nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		return nil
	case 401:
		// Acquire anonymous bearer token from the WWW-Authenticate
		// challenge, retry. ghcr.io and docker.io both work this way
		// for public images.
		token := acquireToken(client, resp.Header.Get("Www-Authenticate"))
		if token == "" {
			return fmt.Errorf("HTTP 401 (cannot obtain anonymous token)")
		}
		req2, _ := http.NewRequest("HEAD", manifestURL, nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		req2.Header.Set("Accept", "application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json")
		resp2, err := client.Do(req2)
		if err != nil {
			return err
		}
		resp2.Body.Close()
		if resp2.StatusCode == 200 {
			return nil
		}
		return fmt.Errorf("HTTP %d after auth", resp2.StatusCode)
	case 404:
		return fmt.Errorf("HTTP 404 (tag may have been removed upstream)")
	case 429:
		return fmt.Errorf("HTTP 429 rate limited (anonymous pull limit hit)")
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
}

// parseImageRef splits an image ref like
//   - "vaultwarden/server:1.32.7" → ("registry-1.docker.io", "vaultwarden/server", "1.32.7")
//   - "ghcr.io/solcreek/grove-apps/pocketbase:0.39.0" → ("ghcr.io", "solcreek/grove-apps/pocketbase", "0.39.0")
//   - "image"                  → ("registry-1.docker.io", "library/image", "latest")
//
// Docker Hub aliases the bare "image" to "library/image" — same
// behavior as `docker pull`. Tag defaults to "latest" when absent.
func parseImageRef(ref string) (registry, repo, tag string) {
	tag = "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		tag = ref[i+1:]
		ref = ref[:i]
	}
	// Registry detection: only treat as registry host if it has a
	// dot/colon AND it's the first path segment. Otherwise it's
	// Docker Hub's namespace.
	first := ""
	if i := strings.Index(ref, "/"); i >= 0 {
		first = ref[:i]
	}
	if strings.ContainsAny(first, ".:") {
		registry = first
		repo = ref[len(first)+1:]
	} else {
		registry = "registry-1.docker.io"
		repo = ref
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
	}
	return
}

// acquireToken parses a WWW-Authenticate Bearer challenge and
// requests an anonymous token. ghcr.io and docker.io return JSON
// {"token": "..."} from the realm URL with the scope query.
func acquireToken(client *http.Client, challenge string) string {
	if !strings.HasPrefix(challenge, "Bearer ") {
		return ""
	}
	params := parseAuthChallenge(challenge[len("Bearer "):])
	realm := params["realm"]
	if realm == "" {
		return ""
	}
	url := realm + "?service=" + params["service"]
	if scope := params["scope"]; scope != "" {
		url += "&scope=" + scope
	}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Token != "" {
		return body.Token
	}
	return body.AccessToken
}

func parseAuthChallenge(s string) map[string]string {
	out := map[string]string{}
	// "realm=\"x\",service=\"y\",scope=\"z\""
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[kv[0]] = strings.Trim(kv[1], `"`)
	}
	return out
}

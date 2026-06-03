package main

import "testing"

// extractRefs is load-bearing for rule-3 and rule-8. Pin the
// behavior on common shapes (single ref, multiple in one string,
// adjacent refs) and on the $${ escape that lets manifests emit
// a literal '${' without triggering interpolation.
func TestExtractRefs(t *testing.T) {
	cases := []struct {
		in   string
		want []interpRef
	}{
		{
			"plain string with no refs",
			nil,
		},
		{
			"${secret.PASSWORD}",
			[]interpRef{{namespace: "secret", name: "PASSWORD"}},
		},
		{
			"postgres://app:${secret.PG_PASS}@db:5432/x",
			[]interpRef{{namespace: "secret", name: "PG_PASS"}},
		},
		{
			"${user.ADMIN_EMAIL}|${grove.public_url}",
			[]interpRef{
				{namespace: "user", name: "ADMIN_EMAIL"},
				{namespace: "grove", name: "public_url"},
			},
		},
		// Escape: $${secret.X} must NOT be treated as a reference,
		// the literal ${secret.X} is what reaches the container.
		{
			"$${secret.FOO}",
			nil,
		},
		// Mixed: escape then real ref
		{
			"$${literal} but ${secret.REAL}",
			[]interpRef{{namespace: "secret", name: "REAL"}},
		},
	}
	for _, c := range cases {
		got := extractRefs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("extractRefs(%q): got %d refs, want %d (got=%v)", c.in, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("extractRefs(%q)[%d] = %v, want %v", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// isV3 is the dispatcher gate. Pin it on what schema_version values
// should route to the v0.3 path (current + patch versions) vs not.
func TestIsV3(t *testing.T) {
	cases := map[string]bool{
		"0.3":     true,
		"0.3.0":   true,
		"0.3.1":   true,
		"0.3.42":  true,
		"0.2":     false,
		"0.4":     false,
		"":        false,
		"0.3.x":   false,
		"v0.3":    false,
		"0.3.0-a": false,
	}
	for in, want := range cases {
		if got := isV3(in); got != want {
			t.Errorf("isV3(%q) = %v, want %v", in, got, want)
		}
	}
}

// shimV3ToManifest carries v0.3 source.image + arch + slug into the
// legacy Manifest struct so Layer 2 (image availability) keeps
// working unchanged. Pin the extraction so future v0.3 schema
// additions don't silently break Layer 2.
func TestShimV3ToManifest(t *testing.T) {
	m := map[string]any{
		"schema_version": "0.3",
		"slug":           "foo/bar",
		"source": map[string]any{
			"type":  "upstream",
			"image": "alpine:3.20",
		},
		"arch": []any{"amd64", "arm64"},
	}
	shim := shimV3ToManifest(m)
	if shim.Slug != "foo/bar" {
		t.Errorf("Slug = %q", shim.Slug)
	}
	if shim.Image != "alpine:3.20" {
		t.Errorf("Image = %q", shim.Image)
	}
	if len(shim.Arch) != 2 || shim.Arch[0] != "amd64" || shim.Arch[1] != "arm64" {
		t.Errorf("Arch = %v", shim.Arch)
	}
}

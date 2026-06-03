package main

import "testing"

// ParseImageRef / parseAuthChallenge tests moved to internal/registry
// when the HEAD-image flow was extracted (shared with cmd/pin).

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// validateManifest pins the user-visible failure modes — each test
// gives a manifest with one specific issue and expects the matching
// error to surface. Without these, a future "tighten the validator"
// PR could silently drop an old check.

func validManifest() *Manifest {
	return &Manifest{
		SchemaVersion: "0.2",
		Slug:          "owner/name",
		Name:          "App",
		Kind:          "image",
		Version:       "1.0.0",
		Description:   "desc",
		Upstream:      "https://github.com/owner/name",
		License:       "MIT",
		Tags:          []string{"tag"},
		Image:         "owner/name:1.0.0",
		Arch:          []string{"amd64", "arm64"},
		Ports:         []Port{{Name: "http", Container: 8080, Protocol: "tcp"}},
		Display:       map[string]any{"category": "dev-tools"},
	}
}

func TestValidateManifest_Valid(t *testing.T) {
	m := validManifest()
	errs := validateManifest("apps/owner/name/manifest.json", m, ".")
	if len(errs) != 0 {
		t.Errorf("clean manifest failed: %v", errs)
	}
}

func TestValidateManifest_SlugPathMismatch(t *testing.T) {
	m := validManifest()
	m.Slug = "different/slug"
	errs := validateManifest("apps/owner/name/manifest.json", m, ".")
	found := false
	for _, e := range errs {
		if e.Field == "slug" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected slug field error, got %v", errs)
	}
}

func TestValidateManifest_RequiredFields(t *testing.T) {
	cases := []struct {
		mutate    func(*Manifest)
		wantField string
	}{
		{func(m *Manifest) { m.SchemaVersion = "" }, "schema_version"},
		{func(m *Manifest) { m.Name = "" }, "name"},
		{func(m *Manifest) { m.Version = "" }, "version"},
		{func(m *Manifest) { m.License = "" }, "license"},
		{func(m *Manifest) { m.Tags = nil }, "tags"},
		{func(m *Manifest) { m.Kind = "" }, "kind"},
		{func(m *Manifest) { m.Image = "" }, "image"},
		{func(m *Manifest) { m.Arch = nil }, "arch"},
		{func(m *Manifest) { m.Ports = nil }, "ports"},
		{func(m *Manifest) { m.Display = nil }, "display"},
	}
	for _, c := range cases {
		m := validManifest()
		c.mutate(m)
		errs := validateManifest("apps/owner/name/manifest.json", m, ".")
		found := false
		for _, e := range errs {
			if e.Field == c.wantField {
				found = true
			}
		}
		if !found {
			t.Errorf("expected error on field %q, got %v", c.wantField, errs)
		}
	}
}

func TestValidateManifest_InvalidPort(t *testing.T) {
	m := validManifest()
	m.Ports = []Port{{Name: "x", Container: 99999, Protocol: "tcp"}}
	errs := validateManifest("apps/owner/name/manifest.json", m, ".")
	if len(errs) == 0 || errs[0].Field != "ports[0].container" {
		t.Errorf("expected port range error, got %v", errs)
	}
}

func TestValidateManifest_UnknownKind(t *testing.T) {
	m := validManifest()
	m.Kind = "source"
	errs := validateManifest("apps/owner/name/manifest.json", m, ".")
	found := false
	for _, e := range errs {
		if e.Field == "kind" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected kind error for unsupported value, got %v", errs)
	}
}

// source.image must match top-level image — a silent mismatch is
// the kind of bug a manual review can miss but produces confusing
// install behavior (grove pulls one image, info shows another).
func TestValidateManifest_SourceImageMismatch(t *testing.T) {
	m := validManifest()
	m.Source = map[string]any{"type": "upstream", "image": "owner/name:9.9.9"}
	errs := validateManifest("apps/owner/name/manifest.json", m, ".")
	found := false
	for _, e := range errs {
		if e.Field == "source.image" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected source.image mismatch error, got %v", errs)
	}
}

// Registry cross-refs catch the most-common contribution mistake:
// add a manifest but forget to add it to registry.json (or vice
// versa).
func TestValidateRegistryCrossRefs_MissingManifest(t *testing.T) {
	reg := &Registry{Apps: []string{"a/b", "c/d"}, Aliases: map[string]string{}}
	manifests := map[string]*Manifest{"a/b": validManifest()}
	errs := validateRegistryCrossRefs("registry.json", reg, manifests)
	found := false
	for _, e := range errs {
		if contains(e.Msg, "c/d") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about c/d missing manifest, got %v", errs)
	}
}

func TestValidateRegistryCrossRefs_UnlistedManifest(t *testing.T) {
	reg := &Registry{Apps: []string{"a/b"}, Aliases: map[string]string{}}
	manifests := map[string]*Manifest{
		"a/b": validManifest(),
		"x/y": validManifest(), // present on disk, not in registry
	}
	errs := validateRegistryCrossRefs("registry.json", reg, manifests)
	found := false
	for _, e := range errs {
		if contains(e.Msg, "x/y") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about unlisted x/y, got %v", errs)
	}
}

func TestValidateRegistryCrossRefs_DanglingAlias(t *testing.T) {
	reg := &Registry{
		Apps: []string{"a/b"},
		Aliases: map[string]string{
			"foo": "missing/app",
		},
	}
	manifests := map[string]*Manifest{"a/b": validManifest()}
	errs := validateRegistryCrossRefs("registry.json", reg, manifests)
	found := false
	for _, e := range errs {
		if contains(e.Msg, "missing/app") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dangling alias error, got %v", errs)
	}
}

func TestValidateRegistryCrossRefs_AliasWithSlash(t *testing.T) {
	reg := &Registry{
		Apps: []string{"a/b"},
		Aliases: map[string]string{
			"foo/bar": "a/b", // alias keys must NOT contain '/'
		},
	}
	manifests := map[string]*Manifest{"a/b": validManifest()}
	errs := validateRegistryCrossRefs("registry.json", reg, manifests)
	found := false
	for _, e := range errs {
		if contains(e.Field, "foo/bar") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected slash-in-alias-key error, got %v", errs)
	}
}

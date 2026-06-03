package registry

import (
	"strings"
	"testing"
)

// ParseImageRef is the load-bearing piece for both Layer 2
// validation and digest pinning — getting it wrong sends HEADs
// to the wrong host or the wrong path, and every check / pin
// reports "404". Pin Docker Hub default + ghcr.io + bare image
// cases.
func TestParseImageRef(t *testing.T) {
	cases := []struct {
		in   string
		host string
		repo string
		tag  string
	}{
		{"vaultwarden/server:1.32.7", "registry-1.docker.io", "vaultwarden/server", "1.32.7"},
		{"alpine:3.20", "registry-1.docker.io", "library/alpine", "3.20"},
		{"vaultwarden/server", "registry-1.docker.io", "vaultwarden/server", "latest"},
		{"ghcr.io/solcreek/grove-apps/pocketbase/pocketbase:0.39.0",
			"ghcr.io", "solcreek/grove-apps/pocketbase/pocketbase", "0.39.0"},
		{"my.registry:5000/foo/bar:v1", "my.registry:5000", "foo/bar", "v1"},
	}
	for _, c := range cases {
		h, r, tg := ParseImageRef(c.in)
		if h != c.host || r != c.repo || tg != c.tag {
			t.Errorf("ParseImageRef(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.in, h, r, tg, c.host, c.repo, c.tag)
		}
	}
}

// parseAuthChallenge survives arbitrary key order + missing scope
// (some registries omit scope for HEAD requests).
func TestParseAuthChallenge(t *testing.T) {
	in := `realm="https://ghcr.io/token",service="ghcr.io",scope="repository:solcreek/grove-apps/pocketbase/pocketbase:pull"`
	got := parseAuthChallenge(in)
	if got["realm"] != "https://ghcr.io/token" {
		t.Errorf("realm = %q", got["realm"])
	}
	if got["service"] != "ghcr.io" {
		t.Errorf("service = %q", got["service"])
	}
	if !strings.Contains(got["scope"], "pocketbase") {
		t.Errorf("scope = %q", got["scope"])
	}
}

// Package registry talks to OCI container registries (Docker Hub,
// ghcr.io, generic v2) for the validator + pin tools. Anonymous
// bearer-token flow for public images — no auth state.
//
// Two operations exposed:
//
//   HeadImage  — does the (registry, repo, tag) point at a real
//                manifest? Used by `cmd/validate` Layer 2.
//   FetchDigest — same flow, but returns the content digest. Used
//                 by `cmd/pin` to replace placeholder image_digest
//                 fields in catalog manifests.
//
// Both share parseImageRef + the WWW-Authenticate challenge flow.
// Public images on docker.io and ghcr.io both follow the same
// 401 → anonymous-token → retry pattern.
package registry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HeadImage returns nil when ref points at a reachable manifest.
// Errors are user-friendly: "HTTP 404 (tag may have been removed
// upstream)", "HTTP 429 rate limited", etc.
func HeadImage(client *http.Client, ref string) error {
	_, _, err := head(client, ref)
	return err
}

// FetchDigest returns the manifest's content-addressable digest
// (sha256:...) for ref. Same auth/network flow as HeadImage but
// reads the Docker-Content-Digest response header.
//
// For multi-arch images this is the digest of the manifest LIST
// (or OCI image index) — which is the right thing to pin because
// the list points at per-arch manifests, and digest-pinning the
// list still lets nerdctl pull the right arch at runtime.
func FetchDigest(client *http.Client, ref string) (string, error) {
	digest, _, err := head(client, ref)
	if err != nil {
		return "", err
	}
	if digest == "" {
		return "", fmt.Errorf("registry returned no Docker-Content-Digest header for %s", ref)
	}
	return digest, nil
}

// head shares the request + auth flow between the two public funcs.
// Returns (digest, status, err). digest is empty when status != 200
// or when the registry didn't return the header.
func head(client *http.Client, ref string) (digest string, status int, err error) {
	registry, repo, tag := ParseImageRef(ref)
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, repo, tag)

	doReq := func(token string) (*http.Response, error) {
		req, _ := http.NewRequest("HEAD", manifestURL, nil)
		req.Header.Set("Accept",
			"application/vnd.docker.distribution.manifest.list.v2+json, "+
				"application/vnd.oci.image.index.v1+json, "+
				"application/vnd.docker.distribution.manifest.v2+json, "+
				"application/vnd.oci.image.manifest.v1+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return client.Do(req)
	}

	resp, err := doReq("")
	if err != nil {
		return "", 0, err
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		return resp.Header.Get("Docker-Content-Digest"), 200, nil
	case 401:
		token := acquireToken(client, resp.Header.Get("Www-Authenticate"))
		if token == "" {
			return "", 401, fmt.Errorf("HTTP 401 (cannot obtain anonymous token for %s)", ref)
		}
		resp2, err := doReq(token)
		if err != nil {
			return "", 0, err
		}
		resp2.Body.Close()
		if resp2.StatusCode == 200 {
			return resp2.Header.Get("Docker-Content-Digest"), 200, nil
		}
		return "", resp2.StatusCode, fmt.Errorf("HTTP %d after auth for %s", resp2.StatusCode, ref)
	case 404:
		return "", 404, fmt.Errorf("HTTP 404 (tag may have been removed upstream) for %s", ref)
	case 429:
		return "", 429, fmt.Errorf("HTTP 429 rate limited (anonymous pull limit) for %s", ref)
	default:
		return "", resp.StatusCode, fmt.Errorf("HTTP %d for %s", resp.StatusCode, ref)
	}
}

// ParseImageRef splits an image ref into (registry, repo, tag):
//   - "vaultwarden/server:1.32.7" → ("registry-1.docker.io", "vaultwarden/server", "1.32.7")
//   - "ghcr.io/foo/bar:0.39"      → ("ghcr.io", "foo/bar", "0.39")
//   - "image"                     → ("registry-1.docker.io", "library/image", "latest")
//
// Docker Hub aliases the bare "image" to "library/image" — same
// behavior as `docker pull`. Tag defaults to "latest" when absent.
func ParseImageRef(ref string) (registry, repo, tag string) {
	tag = "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		tag = ref[i+1:]
		ref = ref[:i]
	}
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
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		v := strings.TrimSpace(kv[1])
		v = strings.TrimPrefix(v, "\"")
		v = strings.TrimSuffix(v, "\"")
		out[strings.TrimSpace(kv[0])] = v
	}
	return out
}

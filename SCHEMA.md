# grove-apps catalog schema

This document defines the on-disk format of the `grove-apps` catalog.
Grove (the CLI) reads from this repo and uses it to install + run
each app inside a sandboxed Linux VM.

**Schema version:** `0.2` (2026-06-02)

## Layout

```
grove-apps/
├── registry.json                        # catalog index + aliases
└── apps/
    └── <owner>/<name>/
        └── manifest.json                # one per app
```

Slugs are `<owner>/<name>` — lowercase, matching the upstream
GitHub (or other forge) repository. Apps published outside GitHub
prefix the owner with the platform hostname:

```
apps/codeberg.org/forgejo/forgejo/manifest.json
apps/gitlab.com/foo/bar/manifest.json
```

Apps built or re-imaged by the Grove team live under
`apps/grove-apps/`.

## `registry.json`

```jsonc
{
  "schema_version": "0.2",
  "apps": [
    "<owner>/<name>",
    ...
  ],
  "aliases": {
    "<short-name>": "<owner>/<name>",
    ...
  }
}
```

Aliases let users type `grove install gitea` instead of
`grove install go-gitea/gitea`. They are explicit, PR-reviewed, and
have no precedence rules — every alias is a hand-curated entry.

When an app's upstream org renames itself, add a redirect alias:
`"old-org/foo": "new-org/foo"` (the value is the new full slug, not
a short name).

## `manifest.json`

Required fields are marked **(required)**.

```jsonc
{
  "schema_version": "0.2",                        // (required)
  "slug": "<owner>/<name>",                       // (required)
  "name": "<display name>",                       // (required)
  "kind": "image",                                // (required) only "image" in v0.2
  "version": "<upstream version>",                // (required)
  "description": "<one-line>",                    // (required)
  "upstream": "<URL of the project>",             // (required)
  "license": "<SPDX id>",                         // (required)
  "tags": ["..."],                                // (required)

  "source": {                                     // (required, see below)
    "type": "upstream" | "mirror" | "build",
    "image": "...",
    ...
  },

  "image": "<image:tag>",                         // (required, matches source.image)
  "image_digest": "sha256:...",                   // optional, pin for safety
  "arch": ["amd64", "arm64"],                     // (required)

  "ports": [                                      // (required, at least one)
    { "name": "http", "container": 3000, "protocol": "tcp" }
  ],
  "volumes": [                                    // optional
    { "name": "data", "path": "/data", "persistent": true }
  ],
  "env": { "KEY": "value" },                      // optional
  "secrets": [                                    // optional
    { "name": "JWT_SECRET", "generate": "random:32" }
  ],

  "health_check": "/path",                        // optional HTTP probe path
  "update_policy": "manual",                      // currently only "manual"

  "display": {                                    // (required)
    "icon": "icons/foo.svg",
    "category": "<category>",
    "long_description": "<paragraph>",
    "screenshots": ["..."]
  },
  "requirements": {
    "min_ram_mb": 128
  },
  "setup": {                                      // optional
    "url_path": "/setup"
  }
}
```

### The `source` block

Every manifest declares how the image got to the user. There are
three types:

**`type: "upstream"`** — the image is published by someone other
than the Grove team. Grove just points at it.

```jsonc
"source": {
  "type": "upstream",
  "image": "vaultwarden/server:1.32.7",
  "maintainer": "official"          // | "community"
}
```

When `maintainer: "community"`, add `maintainer_repo` so users can
inspect who maintains the image:

```jsonc
"source": {
  "type": "upstream",
  "image": "ghcr.io/muchobien/pocketbase:0.39.0",
  "maintainer": "community",
  "maintainer_repo": "https://github.com/muchobien/pocketbase-docker"
}
```

**`type: "mirror"`** — Grove re-hosts an upstream image in our
registry to defeat tag mutation and ensure long-term availability.
The image content matches upstream byte-for-byte (verified by
digest).

```jsonc
"source": {
  "type": "mirror",
  "image": "ghcr.io/grove-apps/uptime-kuma:2.4.0",
  "upstream_image": "louislam/uptime-kuma:2.4.0",
  "mirrored_at": "2026-06-02T13:30:00Z",
  "mirrored_digest": "sha256:..."
}
```

**`type: "build"`** — Grove builds the image from source. The
manifest records exactly what was built and how. Built images live
at `ghcr.io/solcreek/grove-apps/{owner}/{name}:{version}` — the path
mirrors the manifest slug so forks under different owners coexist
without collision.

```jsonc
"source": {
  "type": "build",
  "image": "ghcr.io/solcreek/grove-apps/nexu-io/open-design:0.9.0",
  "repo": "https://github.com/nexu-io/open-design",
  "ref": "open-design-v0.9.0",
  "commit": "<sha>",
  "workspace": "apps/web",                  // optional, monorepo subdir
  "build_command": "pnpm --filter @open-design/web build",
  "output_dir": "apps/web/out",
  "runtime": "static",                      // static | node | python | ruby
  "built_at": "2026-06-02T14:00:00Z",
  "built_digest": "sha256:..."
}
```

### Validation rules

- `slug` must equal `<owner>/<name>` and match the directory path.
- `kind` must be `image` (v0.2 only supports image-based install).
- `source.image` must equal `image` at the top level (the redundancy
  is for human readability; tools should treat them as one).
- `arch` must contain at least one of `amd64`, `arm64`.
- `ports[*].container` must be 1–65535; first port is the one
  surfaced as the app's HTTP endpoint.
- `health_check`, when present, must start with `/`.

### Adding a new app

1. Confirm an upstream-published image exists (or write to the
   maintainers' team to discuss `kind: build`).
2. Verify the image supports both `amd64` and `arm64`.
3. Create `apps/<owner>/<name>/manifest.json` with all required
   fields populated.
4. Add the slug to `registry.json` `apps` array.
5. If a one-word alias is appropriate and unclaimed, add it under
   `aliases`.
6. PR. Manual review will install the app via grove on macOS arm64
   and amd64 before merge.

## Version history

- **0.2** (2026-06-02): `{owner}/{name}` slugs, nested directories,
  `aliases` map in `registry.json`, `source` provenance block on
  every manifest.
- **0.1** (2026-05): flat single-word slugs, no source provenance.

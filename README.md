# grove-apps

The pre-packaged app catalog for **Grove** — a sandboxed app installer
for self-hosted home and personal use. Each app in this repo ships with
a `manifest.json` describing how Grove should install and run it.

> **Status:** v0.1.x — working draft. The manifest schema is mutable
> while Grove is in pre-launch iteration. Breaking changes may land
> without notice until the schema locks at Grove's public launch.

## Apps

| App | Description | Kind | Category |
|---|---|---|---|
| [Vaultwarden](apps/vaultwarden/) | Bitwarden-compatible password manager | image | security |
| [Uptime Kuma](apps/uptime-kuma/) | Self-hosted uptime monitor | source | monitoring |
| [PocketBase](apps/pocketbase/) | Open-source backend + database | source | dev-tools |
| [Ghost](apps/ghost/) | Professional publishing platform | image | productivity |
| [Gitea](apps/gitea/) | Self-hosted Git service | image | dev-tools |
| [Memos](apps/memos/) | Lightweight memo hub | image | knowledge |
| [Excalidraw](apps/excalidraw/) | Virtual whiteboard | image | productivity |
| [Hoppscotch](apps/hoppscotch/) | API development ecosystem | image | dev-tools |
| [AnythingLLM](apps/anythingllm/) | All-in-one AI document chat | image | knowledge |
| [Open Design](apps/open-design/) | AI-driven design tool | image | productivity |
| [Plausible](apps/plausible/) | Privacy-friendly analytics | image | analytics |

## Manifest layout

Each app lives at `apps/<slug>/`:

```
apps/vaultwarden/
├── manifest.json     # v0.1 schema
└── icons/            # icon.svg (rendered in Grove GUI)
```

A manifest declares one of two install paths:

- **`kind: "image"`** — pull a pre-built container image
- **`kind: "source"`** — build from upstream source

See [`apps/vaultwarden/manifest.json`](apps/vaultwarden/manifest.json)
and [`apps/uptime-kuma/manifest.json`](apps/uptime-kuma/manifest.json)
for representative examples of each shape.

## Contributing an app

1. Fork this repo
2. Create `apps/<slug>/manifest.json` matching the v0.1 shape
3. Add the slug to `registry.json`
4. Open a PR

Until Grove publishes a formal schema spec, copy an existing
manifest closest to your app's install style and adapt from there.
Multi-service apps (e.g., something needing Postgres + Redis) are
experimental in v0.1.x — see Plausible's manifest for the current
shape, but expect it to change.

## License

MIT (this catalog). Each individual app retains its own upstream
license — see the `license` field in each manifest.

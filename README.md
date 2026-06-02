# grove-apps

The OSS app catalog for **Grove** — a launcher for developers and builders
who want to **try open-source apps locally and share them publicly** with
a customer or colleague.

Each app in this repo ships with a `manifest.json` describing how Grove
should run it (image, ports, volumes, env, secrets) and how it should
present in the GUI (icon, category, description).

> **Status:** v0.1.x — working draft. Manifest schema is mutable while
> Grove is in pre-launch iteration. Breaking changes may land without
> notice until the schema locks at Grove's public launch.

## Who this is for

Builders and developers, NOT non-technical home users. The use cases
the catalog targets:

- **Demo to a client / customer** — "Here's what a privacy-friendly
  analytics dashboard looks like" (Plausible)
- **Evaluate before adopting** — "Let me try Vaultwarden before
  committing my team's passwords to it"
- **Component in your own work** — "I need a backend for this side
  project" (PocketBase)
- **Show a colleague** — "Look at Ghost's editor, this could replace
  our internal blog setup"

The catalog is not for: home-server appliance use (use Umbrel /
CasaOS), apps with good native macOS `.dmg` distribution (just install
the `.dmg`), stateless web tools (just use the web version).

## Apps

| App | Description | Kind | Category |
|---|---|---|---|
| [Vaultwarden](apps/vaultwarden/) | Bitwarden-compatible password manager | image | security |
| [Memos](apps/memos/) | Lightweight self-hosted memo hub | image | knowledge |
| [Gitea](apps/gitea/) | Self-hosted Git service | image | dev-tools |
| [PocketBase](apps/pocketbase/) | Open-source backend + database | source | dev-tools |
| [Ghost](apps/ghost/) | Professional publishing platform | image | productivity |
| [Plausible](apps/plausible/) | Privacy-friendly analytics | image | analytics |
| [Uptime Kuma](apps/uptime-kuma/) | Self-hosted uptime monitor | source | monitoring |

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
2. Confirm the app fits the curation rule (above)
3. Create `apps/<slug>/manifest.json` matching the v0.1 shape
4. Add the slug to `registry.json`
5. Open a PR

Until Grove publishes a formal schema spec, copy an existing manifest
closest to your app's install style and adapt from there. Multi-service
apps (e.g., something needing Postgres + Redis) are experimental in
v0.1.x — see Plausible's manifest for the current shape, but expect it
to change.

## License

MIT (this catalog). Each individual app retains its own upstream license
— see the `license` field in each manifest.

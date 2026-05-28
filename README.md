# dew-apps

Pre-packaged app catalog for [Dew](https://github.com/solcreek/dew). Install any app with one command.

```bash
dew install uptime-kuma
```

## Apps

| App | Description | Runtime | Port | Size |
|---|---|---|---|---|
| [Uptime Kuma](apps/uptime-kuma/) | Self-hosted monitoring | Node.js | 3001 | ~52MB |
| [Excalidraw](apps/excalidraw/) | Virtual whiteboard | Static | 80 | ~15MB |
| [Vaultwarden](apps/vaultwarden/) | Password manager | Binary | 80 | ~20MB |
| [Gitea](apps/gitea/) | Git service | Binary | 3000 | ~100MB |
| [Memos](apps/memos/) | Memo hub | Binary | 5230 | ~30MB |
| [PocketBase](apps/pocketbase/) | Backend + database | Binary | 8090 | ~15MB |
| [Ghost](apps/ghost/) | Blog platform | Node.js | 2368 | ~80MB |
| [Hoppscotch](apps/hoppscotch/) | API testing | Static | 80 | ~20MB |
| [Plausible](apps/plausible/) | Privacy-friendly analytics | Elixir | 8000 | ~100MB |
| [AnythingLLM](apps/anythingllm/) | AI document chat | Node.js | 3001 | ~200MB |

## How it works

Each app has a `manifest.json` that describes:
- Runtime (Node.js, binary, static)
- Base image for container execution
- Port, volumes, environment variables
- Health check endpoint
- Upstream source + build instructions

Dew downloads a pre-built tarball (not a Docker image), uses a shared base image for the runtime, and starts the app in a container. Second app with the same runtime needs no additional download.

```
First Node.js app:   85MB base (one-time) + 52MB app = 137MB
Second Node.js app:  0MB base (cached)    + 30MB app =  30MB

vs Docker images:
First app:           489MB
Second app:          400MB
```

## Contributing

Add a new app:

1. Create `apps/<name>/manifest.json` following the schema
2. Test with `./build.sh <name>`
3. Open a PR

## Build

```bash
# Build a single app tarball
./build.sh uptime-kuma

# Requires: git, jq, curl, tar
# Some apps also need: npm, docker (for binary extraction)
```

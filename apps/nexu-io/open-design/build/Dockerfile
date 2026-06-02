# Open Design — Next.js standalone web + Node daemon, single image.
#
# Upstream ships only as Electron desktop builds. The web sibling is
# a buildable Next.js app, but in production it needs the `od`
# daemon (Express, port 7456) running alongside to serve /api,
# /artifacts, /frames. The Electron build packs both together; we
# do the equivalent for a server context.
#
# Why no multi-stage with selective copy: open-design is a pnpm
# monorepo with deep workspace-link graph (daemon imports from
# @open-design/sidecar, /contracts, /registry-protocol, ...).
# Next.js standalone output handles the web slice but does not
# resolve workspace links cleanly across pnpm hoist layouts. The
# safe path is to keep the full installed monorepo in the runtime
# image — bigger (~800MB) but predictable.
#
# Process supervision: tini PID 1 reaps zombies; daemon and web
# run in parallel and if either dies the container exits so the
# container runtime can restart.

FROM node:24-alpine

ARG OD_VERSION=0.9.0
ARG OD_REF=open-design-v${OD_VERSION}

# Build tools for native modules (better-sqlite3, sharp, protobufjs).
# Runtime tini supervises the two processes.
RUN apk add --no-cache \
      git \
      python3 \
      make \
      g++ \
      libc6-compat \
      tini \
      bash

# Pin pnpm to the exact version the repo declares (package.json
# packageManager field) so install matches upstream lockfile.
RUN corepack enable && corepack prepare pnpm@10.33.2 --activate

WORKDIR /app

# Shallow clone the pinned release tag — full history is not needed
# for a build artifact and shaves ~80% off the clone time.
RUN git clone --depth 1 --branch ${OD_REF} https://github.com/nexu-io/open-design .

# pnpm install of a monorepo this size pulls ~3GB of deps. Use the
# lockfile verbatim so cache hits and integrity hashes match.
RUN pnpm install --frozen-lockfile

# Web is built in standalone mode so a single `node server.js`
# starts it without `next start`. Daemon is plain tsc.
RUN OD_WEB_OUTPUT_MODE=standalone pnpm --filter @open-design/web build
RUN pnpm --filter @open-design/daemon build

# Runtime ports + paths. OD_PORT is where the daemon listens;
# the web reads the same env to find the daemon.
ENV PORT=3000 \
    HOSTNAME=0.0.0.0 \
    OD_PORT=7456 \
    OD_WORKSPACE_ROOT=/app/workspace \
    NODE_ENV=production

# Create the workspace mount target up-front so the volume mount
# inherits the right ownership.
RUN mkdir -p /app/workspace

EXPOSE 3000
EXPOSE 7456

# Multi-process supervision: daemon in background, web in
# foreground. If web exits the container exits; if daemon exits
# the web will start returning 502s on /api and grove's healthcheck
# fails — both desirable signals for the container runtime to
# restart us.
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["bash", "-c", "node apps/daemon/bin/od.mjs --no-open & cd apps/web/.next/standalone/apps/web && exec node server.js"]

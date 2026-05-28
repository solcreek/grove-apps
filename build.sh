#!/bin/bash
set -euo pipefail

# Build a single app tarball from its manifest.
# Usage: ./build.sh <app-name>

APP="${1:?usage: ./build.sh <app-name>}"
MANIFEST="apps/$APP/manifest.json"

if [ ! -f "$MANIFEST" ]; then
  echo "error: $MANIFEST not found"
  exit 1
fi

WORK="/tmp/dew-build-$APP"
rm -rf "$WORK"
mkdir -p "$WORK"

VERSION=$(jq -r .version "$MANIFEST")
RUNTIME=$(jq -r .runtime "$MANIFEST")
REPO=$(jq -r '.build.repo // empty' "$MANIFEST")
DOCKER_IMAGE=$(jq -r '.docker_image // empty' "$MANIFEST")
DOWNLOAD=$(jq -r '.build.download // empty' "$MANIFEST")
INSTALL=$(jq -r '.build.install // empty' "$MANIFEST")
BUILD_CMD=$(jq -r '.build.build // empty' "$MANIFEST")
OUTPUT=$(jq -r '.build.output // empty' "$MANIFEST")

echo "Building $APP v$VERSION..."

if [ -n "$DOCKER_IMAGE" ] && [ "$RUNTIME" = "binary" ] && [ -z "$DOWNLOAD" ]; then
  # Extract from Docker image
  echo "  Extracting from Docker image: $DOCKER_IMAGE"
  docker pull "$DOCKER_IMAGE" > /dev/null 2>&1
  CONTAINER=$(docker create "$DOCKER_IMAGE")
  docker cp "$CONTAINER:/" "$WORK/rootfs"
  docker rm "$CONTAINER" > /dev/null
elif [ -n "$DOWNLOAD" ]; then
  # Download binary
  URL=$(echo "$DOWNLOAD" | sed "s/\${VERSION}/$VERSION/g")
  echo "  Downloading: $URL"
  curl -fsSL -o "$WORK/download" "$URL"
  if echo "$URL" | grep -q '.zip$'; then
    unzip -q "$WORK/download" -d "$WORK/app"
  else
    mkdir -p "$WORK/app" && tar xzf "$WORK/download" -C "$WORK/app"
  fi
  rm -f "$WORK/download"
elif [ -n "$REPO" ]; then
  # Clone + build
  echo "  Cloning: $REPO"
  git clone --depth 1 --branch "v$VERSION" "$REPO" "$WORK/app" 2>/dev/null || \
  git clone --depth 1 "$REPO" "$WORK/app" 2>/dev/null
  cd "$WORK/app"
  [ -n "$INSTALL" ] && eval "$INSTALL"
  [ -n "$BUILD_CMD" ] && eval "$BUILD_CMD"
  cd -
fi

# Copy manifest
cp "$MANIFEST" "$WORK/manifest.json"

# Create tarball
OUT="apps/$APP/$APP-$VERSION.tar.gz"
if [ -d "$WORK/app" ]; then
  tar czf "$OUT" -C "$WORK/app" . -C "$WORK" manifest.json
elif [ -d "$WORK/rootfs" ]; then
  tar czf "$OUT" -C "$WORK/rootfs" . -C "$WORK" manifest.json
fi

SIZE=$(ls -lh "$OUT" | awk '{print $5}')
SHA=$(sha256sum "$OUT" | awk '{print $1}')
echo "  Output: $OUT ($SIZE, sha256:${SHA:0:12})"

rm -rf "$WORK"

# PocketBase image built from the official upstream release binary.
#
# Upstream ships per-platform zips; we unpack into a minimal Alpine
# image so the runtime layer is ~15MB. Multi-arch handled via the
# buildx TARGETARCH ARG (linux/amd64 + linux/arm64).
#
# Image layout:
#   /pb              WORKDIR; PocketBase writes pb_data/, pb_public/,
#                    pb_hooks/, pb_migrations/ relative to here.
#   /pb is the volume mount surface used by the manifest.

ARG PB_VERSION=0.39.0

FROM alpine:3.20

ARG TARGETARCH
ARG PB_VERSION

RUN apk add --no-cache ca-certificates wget unzip

RUN wget -q -O /tmp/pb.zip \
      "https://github.com/pocketbase/pocketbase/releases/download/v${PB_VERSION}/pocketbase_${PB_VERSION}_linux_${TARGETARCH}.zip" \
 && unzip /tmp/pb.zip -d /usr/local/bin/ \
 && chmod +x /usr/local/bin/pocketbase \
 && rm /tmp/pb.zip

WORKDIR /pb
VOLUME ["/pb"]
EXPOSE 8090

ENTRYPOINT ["/usr/local/bin/pocketbase"]
CMD ["serve", "--http=0.0.0.0:8090"]

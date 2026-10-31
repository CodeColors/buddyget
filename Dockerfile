# syntax=docker/dockerfile:1

# Compilation croisée sur l'architecture de la machine de build (rapide,
# sans émulation), pour amd64 et arm64.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/buddyget ./cmd/buddyget \
 && mkdir /out/data

# Image finale : le binaire statique seul, utilisateur non-root.
FROM scratch
COPY --from=build /out/buddyget /buddyget
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV PORT=8080 DATA_DIR=/data GOMEMLIMIT=100MiB
EXPOSE 8080
VOLUME /data
HEALTHCHECK --interval=60s --timeout=5s --start-period=10s --retries=3 CMD ["/buddyget", "healthcheck"]
ENTRYPOINT ["/buddyget"]

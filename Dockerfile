# syntax=docker/dockerfile:1

# Pin image indexes so both amd64 and arm64 builds use reviewed base images.
# Update the tags and digests together when refreshing security patches.
FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build

WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w" \
    -o /out/gloom ./cmd/gloom

# Includes CA certificates for verified TLS, without a shell or package manager.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5

COPY --from=build --chmod=0555 /out/gloom /usr/local/bin/gloom
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/gloom"]

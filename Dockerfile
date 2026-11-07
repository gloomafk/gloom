# syntax=docker/dockerfile:1

# Pin image indexes so both amd64 and arm64 builds use reviewed base images.
# Update the tags and digests together when refreshing security patches.
FROM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build

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
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

COPY --from=build --chmod=0555 /out/gloom /usr/local/bin/gloom
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/gloom"]

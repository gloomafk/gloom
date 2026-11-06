.PHONY: build test fmt fmt-check fix-check vet check install run

PREFIX ?= $(HOME)/.local

build:
	go build -trimpath -o bin/gloom ./cmd/gloom

test:
	go test -race ./...

fmt:
	gofmt -w .

# Fails on unformatted files.
fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# Fails when go fix would rewrite code to a more modern form.
fix-check:
	@out=$$(go fix -diff ./...); if [ -n "$$out" ]; then echo "$$out"; echo "run: go fix ./..."; exit 1; fi

vet:
	go vet ./...

# The full gate: format, modern Go, vet, then tests.
check: fmt-check fix-check vet test

install: build
	install -d "$(PREFIX)/bin"
	install -m 755 bin/gloom "$(PREFIX)/bin/gloom"

run:
	go run ./cmd/gloom

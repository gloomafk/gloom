.PHONY: build test fmt fmt-check fix-check vet check install run docker-build docker-run

PREFIX ?= $(HOME)/.local
DOCKER ?= docker
DOCKER_IMAGE ?= gloom:local
ARGS ?=
GLOOM_GRAPHICS ?=

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

# Build locally: make docker-build [DOCKER_IMAGE=gloom:local]
docker-build:
	$(DOCKER) build --pull -t "$(DOCKER_IMAGE)" .

# Build and open the terminal: make docker-run
# Pass client flags: make docker-run ARGS='-nick tester -light'
# A terminal is required. Both stdin and a TTY are needed for keyboard input.
# Keep the default seccomp profile, retain no Docker logs, and mount no host data.
docker-run: docker-build
	$(DOCKER) run --rm -it --read-only --cap-drop=ALL \
		--security-opt=no-new-privileges:true --log-driver=none \
		-e TERM -e COLORTERM -e TERM_PROGRAM \
		-e GLOOM_GRAPHICS="$(GLOOM_GRAPHICS)" \
		-e TMUX -e STY -e KITTY_WINDOW_ID \
		"$(DOCKER_IMAGE)" $(ARGS)

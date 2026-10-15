APP_NAME := ymz
# Prefer PATH go; fall back when make inherits a minimal PATH.
GO ?= $(shell if command -v go >/dev/null 2>&1; then command -v go; elif test -x /usr/local/go/bin/go; then echo /usr/local/go/bin/go; else echo go; fi)
COMMANDS := ymz
# User-local install (no root). Override: make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: format format-check vet test lint check all build build-cross build-platforms \
	build-windows-amd64 build-linux-amd64 install uninstall clean

format:
	$(GO) fmt ./...

format-check:
	@set -e; files="$$(find . -type f -name '*.go' \
		-not -path './.git/*' -not -path './.cache/*' -not -path './bin/*' \
		-not -path './dist/*' -not -path './.crush/*')"; \
	unformatted="$$(gofmt -l $$files)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Go formatting check failed:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

lint: format-check vet

check: lint test

all: check build
	./bin/ymz version

build:
	mkdir -p bin
	@set -e; for command in $(COMMANDS); do \
		CGO_ENABLED=0 $(GO) build -trimpath -o "bin/$$command" "./cmd/$$command"; \
	done

build-cross:
	@test -n "$(GOOS_TARGET)" || (echo 'GOOS_TARGET is required' && exit 1)
	@test -n "$(GOARCH_TARGET)" || (echo 'GOARCH_TARGET is required' && exit 1)
	mkdir -p "dist/$(GOOS_TARGET)-$(GOARCH_TARGET)"
	@set -e; for command in $(COMMANDS); do \
		CGO_ENABLED=0 GOOS=$(GOOS_TARGET) GOARCH=$(GOARCH_TARGET) $(GO) build \
			-o "dist/$(GOOS_TARGET)-$(GOARCH_TARGET)/$$command$(EXE_SUFFIX)" "./cmd/$$command"; \
	done

build-windows-amd64:
	$(MAKE) build-cross GOOS_TARGET=windows GOARCH_TARGET=amd64 EXE_SUFFIX=.exe

build-linux-amd64:
	$(MAKE) build-cross GOOS_TARGET=linux GOARCH_TARGET=amd64

build-platforms: build-windows-amd64 build-linux-amd64

# Install the local agent into BINDIR (default: ~/.local/bin).
install: build
	mkdir -p "$(BINDIR)"
	install -m 0755 bin/ymz "$(BINDIR)/ymz"
	@echo "Installed to $(BINDIR): ymz"
	@case ":$$PATH:" in \
		*":$(BINDIR):"*) ;; \
		*) echo "Add to PATH: export PATH=\"$(BINDIR):$$PATH\"" ;; \
	esac

uninstall:
	rm -f "$(BINDIR)/ymz"
	@echo "Removed from $(BINDIR): ymz"


clean:
	rm -rf bin dist
	$(GO) clean -cache

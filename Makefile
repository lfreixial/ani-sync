GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
GOVULNCHECK ?= $(CURDIR)/.tools/govulncheck

# Keep development artifacts in the checkout; do not install tools implicitly.
export GOCACHE := $(CURDIR)/.cache/go-build
export GOMODCACHE := $(CURDIR)/.cache/go-mod
export GOTMPDIR := $(CURDIR)/.cache/tmp
export TMPDIR := $(CURDIR)/.cache/tmp
export GOTOOLCHAIN := local

.PHONY: build test race lint vuln install uninstall prepare

prepare:
	@mkdir -p "$(GOCACHE)" "$(GOMODCACHE)" "$(GOTMPDIR)"

build: prepare
	$(GO) build -buildvcs=false -trimpath -o ani-sync ./cmd/ani-sync

test: prepare
	$(GO) test ./...

race: prepare
	$(GO) test -race ./...

lint: prepare
	$(GO) vet ./...
	@test -z "$$(gofmt -l cmd internal)" || { echo 'Go files need formatting'; exit 1; }

vuln: prepare
	@test -x "$(GOVULNCHECK)" || { echo 'govulncheck is not installed; see docs/DEVELOPMENT.md'; exit 1; }
	$(GOVULNCHECK) ./...

install: build
	install -Dm755 ani-sync "$(DESTDIR)$(BINDIR)/ani-sync"

uninstall:
	rm -f -- "$(DESTDIR)$(BINDIR)/ani-sync"

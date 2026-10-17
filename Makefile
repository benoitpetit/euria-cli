GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BIN ?= bin/euria

.PHONY: help build install check test

help:
	@printf '%s\n' \
	  'Euria Retro Go CLI' \
	  '' \
	  'Targets:' \
	  '  build    Build $(BIN)' \
	  '  install  Install the executable into $(BINDIR)' \
	  '  check    Check Go formatting and run go vet' \
	  '  test     Run go test ./...'

build:
	@mkdir -p bin
	$(GO) build -o $(BIN) ./cmd/euria

install: build
	@mkdir -p "$(BINDIR)"
	install -m 0755 $(BIN) "$(BINDIR)/euria"

check:
	@unformatted="$$(gofmt -l $$(find cmd internal -type f -name '*.go'))"; \
	  if [ -n "$$unformatted" ]; then printf '%s\n' "$$unformatted"; exit 1; fi
	$(GO) vet ./...

test:
	$(GO) test ./...

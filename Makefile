# Development shortcuts. GO can point to a toolchain outside PATH (e.g. ~/.local/go/bin/go).
GO ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
BINARY := bin/atlas-schema-desk

.PHONY: install frontend build vet test integration check clean

# From a fresh clone: toolchains (if missing), build, first connection, check.
install:
	./install.sh

frontend:
	npm ci --prefix frontend
	npm run build --prefix frontend

build:
	$(GO) build -trimpath -o $(BINARY) ./cmd/atlas-schema-desk

vet:
	$(GO) vet ./...
	unformatted=$$("$$($(GO) env GOROOT)/bin/gofmt" -l cmd internal web) && test -z "$$unformatted" || { echo "gofmt: $$unformatted"; exit 1; }

test: vet
	$(GO) test -race ./...

# Disposable PostgreSQL/MySQL databases, Go integration tests and Firefox checks.
integration:
	GO=$(GO) bash tests/run.sh

check: test build
	python3 scripts/check_public.py
	python3 scripts/check_distribution.py $(BINARY)

clean:
	rm -rf bin web/dist/app

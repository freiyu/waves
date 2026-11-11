# Entry point for build and checks. Go tools live in ~/.local/go/bin on the dev machine.
GO ?= go
VERSION ?= 0.1.0-dev
.PHONY: build release gen web-build web-check web-test web-e2e test check check-release clean verify

# The binary embeds web/dist; Go builds and tests need it to exist.
web/dist/index.html:
	$(MAKE) web-build

build: web-build ## static binary with the dashboard embedded
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/waves ./cmd/waves

# Static Linux binaries for amd64 and arm64, their checksums and the install script, in release/. Name the version: make release VERSION=1.0.0
release:
	@case "$(VERSION)" in ""|*-dev) echo "name the release version, for example: make release VERSION=1.0.0"; exit 1;; esac
	@if git rev-parse --git-dir >/dev/null 2>&1 && [ -n "$$(git status --porcelain)" ]; then echo "uncommitted changes: build a release from a clean checkout"; exit 1; fi
	$(MAKE) web-build check-release
	rm -rf release && mkdir release
	for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o release/waves-linux-$$arch ./cmd/waves || exit 1; \
	done
	cp install.sh release/install.sh
	cd release && sha256sum waves-linux-amd64 waves-linux-arm64 > SHA256SUMS && cat SHA256SUMS

gen: ## regenerate the OpenAPI document and the web client types from the Go contract
	$(GO) run ./scripts/genapi web/openapi.json
	cd web && npx openapi-typescript openapi.json -o src/api/types.d.ts

# A fresh checkout has no node_modules: install exactly what package-lock.json pins, once.
web/node_modules:
	cd web && npm ci

web-build: | web/node_modules
	cd web && npm run build

web-check:
	cd web && npm run check && npx tsc --noEmit -p .

web-test:
	cd web && npm test

web-e2e:
	cd web && npm run e2e

test: web/dist/index.html
	$(GO) test -race ./...

check: web/dist/index.html
	gofmt -l . | (! grep .)
	$(GO) vet ./...
	$(MAKE) test gen
	git diff --quiet -- web/openapi.json web/src/api/types.d.ts || echo 'note: regenerated files differ from the index'
	$(MAKE) web-check web-test web-build check-release

verify: ## every suite: gate-bypass first, Go unit, end-to-end on the real binary (incl. real-time lease timers, crash injection, budget, Playwright on the real service), web checks
	GO=$(GO) ./scripts/verify.sh

clean:
	rm -rf web/dist web/.mock-dist bin

# The embedded bundle must talk to the real service: no mock adapter, fixture secret or test hook in it.
check-release:
	test -f web/dist/index.html
	! grep -rlE 'mock-passphrase|__wavesMock|link-valid-demo' web/dist

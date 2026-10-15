.PHONY: build install package distribution-check mapbox-check dev pre-commit format-check check test race interactions fixtures bench
ARGS ?= fixtures/example.gpx
PREFIX ?= $(HOME)/.local
VERSION ?= dev
BUILD_VCS ?= auto
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
ARCHIVE = mapbox-tui-$(VERSION)-linux-amd64.tar.gz
build:
	mkdir -p bin
	cd go && go build -trimpath -buildvcs=$(BUILD_VCS) -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o ../bin/mapbox-tui .
mapbox-check:
	python3 scripts/check_mapbox_contract.py
distribution-check:
	@test "$$(go env GOOS)/$$(go env GOARCH)" = linux/amd64 || { echo "Installation and packaging require the audited linux/amd64 target." >&2; exit 1; }
install: distribution-check build
	install -d "$(DESTDIR)$(PREFIX)/bin" "$(DESTDIR)$(PREFIX)/share/licenses/mapbox-tui"
	install -m 755 bin/mapbox-tui "$(DESTDIR)$(PREFIX)/bin/mapbox-tui"
	install -m 644 LICENSE THIRD_PARTY_NOTICES.txt "$(DESTDIR)$(PREFIX)/share/licenses/mapbox-tui/"
# Explicit VERSION/COMMIT metadata stays stable before and after tag creation.
package: BUILD_VCS = false
# Keep local and Actions package builds identical, ignoring user Go configuration.
package: export GOENV = off
package: export GOFLAGS =
package: export GOEXPERIMENT =
package: export GOAMD64 = v1
package: export GOWORK = off
package: export GOTOOLCHAIN = local
package: export CGO_ENABLED = 0
package: distribution-check build
	mkdir -p dist
	tar --mode='a=rX,u+w' -czf "dist/$(ARCHIVE)" LICENSE THIRD_PARTY_NOTICES.txt -C docs INSTALL.md -C ../bin mapbox-tui
	cd dist && sha256sum "$(ARCHIVE)" > "$(ARCHIVE).sha256"
dev:
	python3 scripts/dev.py $(ARGS)
pre-commit:
	git diff --cached --check
	python3 scripts/check_format.py --staged
format-check:
	python3 scripts/check_format.py
check: format-check test race
test: build
	MAPBOX_ACCESS_TOKEN='' go test ./...
	MAPBOX_ACCESS_TOKEN='' go vet ./...
	MAPBOX_ACCESS_TOKEN='' python3 scripts/validate.py
race:
	MAPBOX_ACCESS_TOKEN='' go test -race ./...
interactions: build
	MAPBOX_ACCESS_TOKEN='' python3 scripts/interactions.py
fixtures:
	python3 scripts/fixtures.py
bench: build fixtures
	python3 scripts/benchmark.py

# Maintainer release command; not included in viewer archives.
.PHONY: release release-interactions
RELEASE_ARGS ?=
release:
	go run ./cmd/release $(RELEASE_ARGS)
release-interactions:
	mkdir -p bin
	go build -o bin/mapbox-release ./cmd/release
	python3 scripts/release_interactions.py

.PHONY: build install package distribution-check mapbox-check dev pre-commit format-check check test race interactions fixtures bench
ARGS ?= fixtures/example.gpx
PREFIX ?= $(HOME)/.local
build:
	mkdir -p bin
	cd go && go build -trimpath -o ../bin/mapbox-tui .
mapbox-check:
	python3 scripts/check_mapbox_contract.py
distribution-check:
	@test "$$(go env GOOS)/$$(go env GOARCH)" = linux/amd64 || { echo "Installation and packaging require the audited linux/amd64 target." >&2; exit 1; }
	@test "$$(go env GOVERSION)" = "$$(awk '$$1 == "go" { print "go" $$2; exit }' go.mod)" || { echo "Installation and packaging require the Go version in go.mod." >&2; exit 1; }
install: distribution-check build
	install -d "$(DESTDIR)$(PREFIX)/bin" "$(DESTDIR)$(PREFIX)/share/licenses/mapbox-tui"
	install -m 755 bin/mapbox-tui "$(DESTDIR)$(PREFIX)/bin/mapbox-tui"
	install -m 644 LICENSE THIRD_PARTY_NOTICES.txt "$(DESTDIR)$(PREFIX)/share/licenses/mapbox-tui/"
package: distribution-check build
	mkdir -p dist
	tar -czf "dist/mapbox-tui-$$(go env GOOS)-$$(go env GOARCH).tar.gz" LICENSE THIRD_PARTY_NOTICES.txt -C bin mapbox-tui
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

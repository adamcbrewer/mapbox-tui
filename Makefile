.PHONY: build install package mapbox-check dev pre-commit format-check check test race interactions fixtures bench
ARGS ?= fixtures/example.gpx
PREFIX ?= $(HOME)/.local
build:
	mkdir -p bin
	cd go && go build -trimpath -o ../bin/shoe .
mapbox-check:
	python3 scripts/check_mapbox_contract.py
install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 755 bin/shoe "$(DESTDIR)$(PREFIX)/bin/shoe"
package: build
	mkdir -p dist
	tar -czf "dist/shoe-$$(go env GOOS)-$$(go env GOARCH).tar.gz" -C bin shoe
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

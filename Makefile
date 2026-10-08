.PHONY: build dev pre-commit format-check check test race interactions fixtures bench
ARGS ?= fixtures/example.gpx
build:
	mkdir -p bin
	cd go && go build -trimpath -o ../bin/shoe .
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

.PHONY: build dev test fixtures bench
ARGS ?= fixtures/example.gpx
build:
	mkdir -p bin
	cd go && go build -trimpath -o ../bin/shoe .
dev:
	python3 scripts/dev.py $(ARGS)
test: build
	go test ./... && go vet ./...
	python3 scripts/validate.py
fixtures:
	python3 scripts/fixtures.py
bench: build fixtures
	python3 scripts/benchmark.py

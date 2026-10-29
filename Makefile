.PHONY: install web build fmt fmt-check test vet lint check package clean

VERSION ?= dev

install:
	pnpm --dir web install --frozen-lockfile

web:
	pnpm --dir web build

build: web
	go build -trimpath -o bin/mihomo-capture ./cmd/mihomo-capture

fmt:
	gofmt -w cmd internal web/*.go

fmt-check:
	@test -z "$$(gofmt -l cmd internal web/*.go)" || { gofmt -l cmd internal web/*.go; exit 1; }

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	pnpm --dir web lint

check: fmt-check test vet lint build

package: web
	scripts/package.sh "$(VERSION)"

# Runtime configuration, identities, and capture data are kept.
clean:
	rm -rf bin dist web/dist/assets web/dist/index.html web/dist/favicon.svg

.PHONY: build test fmt fmt-check vet ci install release-smoke clean

GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags "-X main.version=$(VERSION)" -o amq-noc ./cmd/amq-noc

test:
	go test ./...

fmt:
	gofmt -w $(GO_FILES)

fmt-check:
	@test -z "$(shell gofmt -l $(GO_FILES))"

vet:
	go vet ./...

ci: fmt-check vet test

install: build
	install amq-noc $(GOPATH)/bin/amq-noc 2>/dev/null || install amq-noc $(HOME)/go/bin/amq-noc

release-smoke:
	@test "$(VERSION)" != "dev" || (echo "VERSION is required, for example VERSION=v0.1.0" >&2; exit 1)
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	GOBIN="$$tmp" GOPROXY=direct go install github.com/omriariav/amq-noc/cmd/amq-noc@$(VERSION); \
	got="$$("$$tmp/amq-noc" version)"; \
	want="amq-noc $(VERSION)"; \
	if [ "$$got" != "$$want" ]; then \
		echo "version mismatch: got '$$got', want '$$want'" >&2; \
		exit 1; \
	fi; \
	echo "$$got"

clean:
	rm -f amq-noc

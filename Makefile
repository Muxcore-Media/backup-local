.PHONY: build test lint clean fmt tidy proto docker docker-push ci help

GO ?= go
PROTOC ?= protoc
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
LDFLAGS ?= -s -w -X main.version=$(VERSION)
BINARY ?= backup-local

build:
	$(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/module

test:
	$(GO) test -race -count=1 -timeout 60s ./...

lint:
	golangci-lint run --timeout 120s ./...

clean:
	rm -f $(BINARY)
	rm -f cmd/module/module
	rm -rf dist/

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

proto:
	$(PROTOC) --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		-I proto proto/muxcore/backup/v1/backup.proto

docker:
	docker build -t ghcr.io/yourorg/$(BINARY):$(VERSION) .
	docker tag ghcr.io/yourorg/$(BINARY):$(VERSION) ghcr.io/yourorg/$(BINARY):latest

docker-push: docker
	docker push ghcr.io/yourorg/$(BINARY):$(VERSION)
	docker push ghcr.io/yourorg/$(BINARY):latest

ci: lint test build

help:
	@echo "Targets:"
	@echo "  build       - compile the module binary"
	@echo "  test        - run tests with race detection"
	@echo "  lint        - golangci-lint"
	@echo "  proto       - regenerate backup gRPC stubs"
	@echo "  clean       - remove build artifacts"
	@echo "  fmt         - format Go source"
	@echo "  tidy        - go mod tidy"
	@echo "  docker      - build Docker image"
	@echo "  docker-push - build and push Docker image"
	@echo "  ci          - lint + test + build"

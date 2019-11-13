.PHONY: deps docs
.EXPORT_ALL_VARIABLES:

GO111MODULE ?= on
LOCALS      := $(shell find . -type f -name '*.go')
BIN         ?= reacter-$(shell go env GOOS)-$(shell go env GOARCH)

all: deps test build docs docker

deps:
	go get ./...
	-go mod tidy

fmt:
	go generate -x ./...
	gofmt -w $(LOCALS)
	go vet ./...

test:
	go test ./...

build: fmt
	go build -o bin/$(BIN) cmd/reacter/main.go
	which reacter && cp -v bin/$(BIN) `which reacter` || true

docker:
	which docker && docker build .

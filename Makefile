.PHONY: deps docs
.EXPORT_ALL_VARIABLES:

GO111MODULE ?= on
LOCALS      := $(shell find . -type f -name '*.go')
BIN         ?= reacter-$(shell go env GOOS)-$(shell go env GOARCH)

all: deps test build docs

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
	GOOS=linux GOARCH=amd64 go build -o bin/reacter-linux-amd64 cmd/reacter/main.go
	GOOS=linux GOARCH=arm go build -o bin/reacter-linux-arm cmd/reacter/main.go
	which reacter && cp -v bin/$(BIN) `which reacter` || true

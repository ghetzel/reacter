.PHONY: deps docs
.EXPORT_ALL_VARIABLES:

GO111MODULE ?= on
LOCALS      := $(shell find . -type f -name '*.go')

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
	go build -o bin/reacter cmd/reacter/main.go
	which reacter && cp -v bin/reacter `which reacter` || true
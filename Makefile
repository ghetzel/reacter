.PHONY: test deps

all: fmt deps build

deps:
	@go list golang.org/x/tools/cmd/goimports || go get golang.org/x/tools/cmd/goimports
	go generate -x
	go get .

clean:
	-rm -rf bin

fmt:
	goimports -w .
	go vet .

test:
	go test .

build: fmt
	go build -o bin/`basename ${PWD}` cli/*.go

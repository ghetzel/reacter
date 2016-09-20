all: vendor fmt build

update:
	test -d vendor && rm -rf vendor || exit 0
	glide up --strip-vcs --update-vendored

vendor:
	go list github.com/Masterminds/glide
	glide install --strip-vcs --update-vendored

fmt:
	gofmt -w .
	gofmt -w cli/

build: fmt
	go build -o bin/`basename ${PWD}` cli/*.go

clean:
	rm -rf vendor bin

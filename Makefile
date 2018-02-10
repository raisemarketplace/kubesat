GO = go
GOFMT = gofmt
LINT = gometalinter

LINT_DEADLINE = 5s

PROGRAM = kubesat
VERSION = $(shell git describe --match [0-9]*)

default : build test

# Rather than use ldflags to set this at compile time, generate the
# file. This way folks using `go get` can get a reasonable version
# number without building via this Makefile
version.go : Makefile
	printf "package main\nconst(\nProgram=\"%s\"\nVersion=\"%s\"\n)" $(PROGRAM) $(VERSION) \
		| gofmt /dev/stdin >$@
.PHONY : version.go

test :
	$(GO) test -v ./...

lint :
	$(LINT) --vendor --deadline=$(LINT_DEADLINE) ./...

build : version.go
	$(GO) install

.PHONY : build lint test

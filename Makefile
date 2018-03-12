GO = go
GOFMT = gofmt
LINT = gometalinter

LINT_DEADLINE = 5s

PROGRAM = kubesat
VERSION = $(shell git describe --match [0-9]*)

BUILD_FLAGS := -ldflags "-X main.Version=$(VERSION) -X main.Program=$(PROGRAM)"

default : build test

test :
	$(GO) test -v ./...

lint :
	$(LINT) --vendor --deadline=$(LINT_DEADLINE) ./...

install:
	$(GO) install $(BUILD_FLAGS)

build :
	$(GO) build $(BUILD_FLAGS)

.PHONY : build lint test

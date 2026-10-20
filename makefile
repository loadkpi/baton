# Baton build and check targets. See AGENTS.md for when to use which.

export CGO_ENABLED = 0

.PHONY: build test-only test lint fmt vet clean

# build compiles the single static baton binary into ./bin/baton.
build:
	go build -o bin/baton ./app/baton

# test-only runs every test. The tests build the binary themselves.
test-only:
	go test ./...

# lint runs golangci-lint with the default configuration.
lint:
	golangci-lint run

# fmt rewrites all Go files in gofmt style.
fmt:
	gofmt -w .

vet:
	go vet ./...

# test is the full check: tests, then lint.
test: test-only lint

clean:
	rm -rf bin

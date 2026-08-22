GO ?= go
BINARY := passport-check

.PHONY: all build run test lint vet tidy fmt clean

all: build

build:
	$(GO) build -o $(BINARY) ./cmd/passport-check

run: build
	./$(BINARY)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run \
		|| $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

tidy:
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

clean:
	rm -f $(BINARY)

.PHONY: build run test lint clean

BIN_DIR := bin
BINARY  := $(BIN_DIR)/csar-notify

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BINARY) ./cmd/csar-notify

run: build
	$(BINARY)

test:
	go test ./... -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN_DIR)

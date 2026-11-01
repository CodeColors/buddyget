BIN := bin/buddyget

.PHONY: build run test lint e2e clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/buddyget

run: build
	DATA_DIR=./data LOG_LEVEL=debug ./$(BIN)

test:
	go test -race ./...

lint:
	go vet ./...
	golangci-lint run ./...

e2e: build
	cd e2e && npm ci && npm test

clean:
	rm -rf bin data e2e/node_modules e2e/screenshots

.PHONY: all build run-mind run-face run-hand clean test lint

# ── Default ──
all: build

# ── Build all binaries ──
build:
	@echo "building all modules..."
	@mkdir -p bin
	cd modules/half-pi-mind && go build -o ../../bin/half-pi-mind ./cmd/half-pi-mind/
	cd modules/half-pi-face && go build -o ../../bin/half-pi-face ./cmd/half-pi-face/
	cd modules/half-pi-hand && go build -o ../../bin/half-pi-hand ./cmd/half-pi-hand/
	@echo "→ bin/half-pi-mind"
	@echo "→ bin/half-pi-face"
	@echo "→ bin/half-pi-hand"

# ── Run modules ──
run-mind:
	go run ./modules/half-pi-mind/cmd/half-pi-mind/

run-face:
	go run ./modules/half-pi-face/cmd/half-pi-face/

run-hand:
	go run ./modules/half-pi-hand/cmd/half-pi-hand/

# ── Test all modules ──
test:
	cd modules/half-pi-mind && go test -race -count=1 ./...

# ── Lint ──
lint:
	@which golangci-lint > /dev/null 2>&1 || (echo "install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1)
	golangci-lint run ./modules/...

# ── Clean ──
clean:
	rm -rf bin/
	-find . -name 'half-pi-mind' -type f -delete
	-find . -name 'half-pi-face' -type f -delete
	-find . -name 'half-pi-hand' -type f -delete

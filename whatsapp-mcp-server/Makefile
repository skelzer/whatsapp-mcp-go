VERSION ?= 0.0.1
BINARY  ?= whatsapp-mcp
DOCKER_USER ?=

install:
	go get ./...

# Native build for the current OS/arch.
build:
	CGO_ENABLED=0 go build -o $(BINARY) .

# Cross-compiled builds.
build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o $(BINARY).exe .

build-mac:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o $(BINARY)-darwin-arm64 .

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux-amd64 .

run:
	go run .

# Build then launch the interactive connection wizard.
connect: build
	./$(BINARY) connect

tidy:
	go mod tidy

test:
	go test ./...

image:
	docker buildx build --platform linux/amd64,linux/arm64 -t $(DOCKER_USER)/whatsapp-mcp-server:v$(VERSION) --push .

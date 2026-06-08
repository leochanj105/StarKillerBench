.PHONY: build test image

build:
	go generate ./... && go build ./...

test:
	go test ./...

image:
	docker build -t agentbench/auth:dev .

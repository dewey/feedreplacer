PORT ?= 8080

.PHONY: dev test docker

dev:
	PORT=$(PORT) go run .

test:
	go vet ./...
	go test -race ./...

docker:
	docker build -t feedreplacer .

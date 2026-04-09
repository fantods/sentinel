.PHONY: build run test lint docker-up docker-down

build:
	go build -o bin/sentinel ./cmd/sentinel

run: build
	./bin/sentinel

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

docker-build:
	docker build -t sentinel:latest .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f sentinel

tidy:
	go mod tidy

fmt:
	gofmt -w .
	goimports -w .

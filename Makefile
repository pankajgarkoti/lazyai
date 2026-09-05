.PHONY: build test run

build:
	go build -o bin/lazyai ./cmd/lazyai

test:
	go test ./...

run: build
	./bin/lazyai --dir $${DIR:-.}

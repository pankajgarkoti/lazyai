.PHONY: build release test run

build:
	go build -o bin/lazyai ./cmd/lazyai

release:
	go build -trimpath -ldflags="-s -w" -o bin/lazyai ./cmd/lazyai

test:
	go test ./...

run: build
	./bin/lazyai --dir $${DIR:-.}

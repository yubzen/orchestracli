.PHONY: build test release clean

VERSION := 1.0.0
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o orchestra ./cmd/orchestra

test:
	go test ./...

release:
	goreleaser release --snapshot --clean

clean:
	rm -f orchestra
	rm -rf dist/
	rm -f orchestra_vss.db orchestra_vss.db-shm orchestra_vss.db-wal

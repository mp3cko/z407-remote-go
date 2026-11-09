.PHONY: all build windows test vet fmt clean
all: build

build:
	go build -trimpath -ldflags="-s -w" -o dist/z407 ./cmd/z407

windows:
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/Z407_Control_Windows.exe ./cmd/z407

test:
	go test -race -cover ./...

vet:
	go vet ./...
	test -z "$$(gofmt -l .)"

clean:
	rm -rf dist

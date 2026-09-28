.PHONY: build test integration check
build:
	go build -o jj-patch .
test:
	go test -race ./...
integration: build
	python3 -m unittest discover -s integration -v
check: test integration
	go vet ./...

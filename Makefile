.PHONY: build debug main
build:
	docker build -t bistro .
debug:
	docker run --rm -it \
		-w /bistro \
		-v $(PWD):/bistro golang:1.27-trixie bash
main:
	go build -o main .

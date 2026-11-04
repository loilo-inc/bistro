.PHONY: debug main
IMAGE := bistro
debug:
	docker run --rm -it \
		-w /bistro \
		-v $(PWD):/bistro golang:1.27-trixie bash
main:
	go build -o main .
build:
	docker build -t $(IMAGE) .

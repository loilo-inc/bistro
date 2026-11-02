.PHONY: debug main
debug:
	docker run --rm -it \
		-w /bistro \
		-v $(PWD):/bistro golang:1.27-trixie bash
main:
	go build -o main .
ldd:
	go build -o ldd cmd/ldd/main.go

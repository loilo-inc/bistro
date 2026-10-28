build:
	docker build -t bistro .
debug: build
	docker run --rm -it bistro

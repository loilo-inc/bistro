.PHONY: debug main
IMAGE := bistro
VERSION := dev
COMMIT := none
DATE := unknown

debug:
	docker run --rm -it \
		-w /bistro \
		-v $(PWD):/bistro golang:1.27-trixie bash
main:
	go build -o main \
		-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"
build:
	docker build -t $(IMAGE) . \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE)

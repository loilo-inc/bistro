FROM golang:1.27-trixie AS builder
COPY . /app
WORKDIR /app
RUN go mod tidy
RUN go build -o bistro . && \
    chmod +x bistro && \
    mv ./bistro /usr/local/bin/ && \
    bistro bundle -o bistro.tar /usr/local/bin/bistro && \
    mkdir rootfs && \
    tar -xf bistro.tar -C rootfs

FROM scratch
COPY --from=builder /app/rootfs /
ENTRYPOINT ["/usr/local/bin/bistro"]

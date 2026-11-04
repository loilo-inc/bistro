FROM golang:1.27-trixie AS builder
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
COPY . /app
WORKDIR /app
RUN go mod tidy
RUN make main VERSION="$VERSION" COMMIT="$COMMIT" DATE="$DATE" && \
    chmod +x main && \
    mv ./main /usr/local/bin/bistro && \
    bistro bundle -o bistro.tar /usr/local/bin/bistro && \
    mkdir rootfs && \
    tar -xf bistro.tar -C rootfs

FROM scratch
COPY --from=builder /app/rootfs /
ENTRYPOINT ["/usr/local/bin/bistro"]

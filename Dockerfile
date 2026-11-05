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
    bistro bundle /usr/local/bin/bistro \
        -o bistro.tar \
        --package base-files,ca-certificates \
        --copy /etc/ssl && \
    bistro unpack bistro.tar -d rootfs

FROM scratch
COPY --from=builder /app/rootfs /
ENTRYPOINT ["/usr/local/bin/bistro"]

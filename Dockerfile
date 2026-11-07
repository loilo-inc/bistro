FROM golang:1.27-trixie AS builder
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
COPY . /app
WORKDIR /app
RUN go mod tidy
RUN make main VERSION="$VERSION" COMMIT="$COMMIT" DATE="$DATE" && \
    chmod +x main && \
    mv ./main /bin/bistro && \
    bistro bundle /bin/bistro  -o bistro.tar --package base-files && \
    mkdir rootfs && tar -xf bistro.tar -C rootfs

FROM scratch
COPY --from=builder /app/rootfs /
ENTRYPOINT ["/bin/bistro"]

FROM golang:1.27-trixie

WORKDIR /app
COPY go.mod go.sum main.go /app/
COPY pkg /app/pkg/
RUN go mod tidy
RUN go build -o /app/bistro main.go

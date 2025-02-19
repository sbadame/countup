FROM golang:1.24 AS builder

WORKDIR /app

# pre-copy/cache go.mod for pre-downloading dependencies and only redownloading them in subsequent builds if they change
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -v -o /app ./...

FROM alpine:latest as release

WORKDIR /
COPY --from=builder /app/countdown .
EXPOSE 8080
ENTRYPOINT ["/countdown"]

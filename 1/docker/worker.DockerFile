FROM golang:alpine AS builder
WORKDIR /build
COPY . .

RUN go mod tidy && \
    go build -o worker ./cmd/worker/main.go

FROM alpine
WORKDIR /build

COPY --from=builder /build/worker /build/worker
COPY --from=builder /build/configs /build/configs

CMD ./worker --config ./configs/worker.yaml
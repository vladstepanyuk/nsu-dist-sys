FROM golang:alpine AS builder
WORKDIR /build
COPY . .

RUN go mod tidy && \
    go build -o worker ./cmd/worker/main.go

FROM alpine
WORKDIR /build

COPY --from=builder /build/worker /build/worker

ENV WORKER_RABBITMQ_URI=""

CMD ./worker --config /etc/worker/configs/worker.yaml --rabbitmq_uri $WORKER_RABBITMQ_URI
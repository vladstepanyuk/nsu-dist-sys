FROM golang:alpine AS builder
WORKDIR /build
COPY . .


RUN go mod tidy && \
    go build -o manager ./cmd/manager/main.go

FROM alpine
WORKDIR /build

COPY --from=builder /build/manager /build/manager

ENV MANAGER_PORT=1234
ENV MANAGER_RABBITMQ_URI=""

CMD ./manager --port $MANAGER_PORT --config /etc/manager/configs/manager.yaml --rabbitmq_uri $MANAGER_RABBITMQ_URI
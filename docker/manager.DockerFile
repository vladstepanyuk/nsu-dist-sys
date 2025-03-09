FROM golang:alpine AS builder
WORKDIR /build
COPY . .


RUN go mod tidy && \
    go build -o manager ./cmd/manager/main.go

FROM alpine
WORKDIR /build

COPY --from=builder /build/manager /build/manager
COPY --from=builder /build/configs /build/configs

ENV PORT=1234

CMD ./manager --port $PORT --config ./configs/manager.yaml
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY . .

RUN go build -o /app/bin/server ./services/order-service

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/bin/server .
EXPOSE 50054
CMD ["./server"]

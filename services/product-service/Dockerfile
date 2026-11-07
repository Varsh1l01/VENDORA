FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY . .

RUN go build -o /app/bin/server ./services/product-service

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/bin/server .
EXPOSE 50052
CMD ["./server"]

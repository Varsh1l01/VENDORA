FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY . .

COPY services/api-gateway/docs ./services/api-gateway/docs

RUN go build -o /app/bin/server ./services/api-gateway

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/bin/server .
EXPOSE 8080
CMD ["./server"]

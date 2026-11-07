# syntax=docker/dockerfile:1.7
# One parameterized image definition for every service:
#   docker build --build-arg SERVICE=order-service -t vendora/order-service .
FROM golang:1.26-alpine AS build
WORKDIR /src
# Dependencies first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY gen ./gen
COPY pkg ./pkg
COPY services ./services
ARG SERVICE
RUN test -n "$SERVICE" || (echo "SERVICE build-arg is required" && exit 1)
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./services/${SERVICE}

# Distroless: no shell or package manager, runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /app/server
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]

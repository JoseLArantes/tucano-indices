# syntax=docker/dockerfile:1.7

FROM golang:1.27.4-alpine AS builder
RUN apk add --no-cache ca-certificates git build-base
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/index-sync ./cmd/index-sync
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/scheduler ./cmd/scheduler

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl && adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --from=builder /out/index-sync /app/index-sync
COPY --from=builder /out/scheduler /app/scheduler
COPY api/openapi.yaml /app/api/openapi.yaml
USER appuser
EXPOSE 8080
ENV OPENAPI_SPEC_PATH=/app/api/openapi.yaml
ENTRYPOINT ["/app/server"]

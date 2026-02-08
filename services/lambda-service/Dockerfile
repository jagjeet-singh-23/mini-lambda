# Build stage
FROM golang:1.25.7-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Dependency caching
COPY shared/go.mod shared/go.sum ./shared/
COPY services/lambda-service/go.mod services/lambda-service/go.sum ./services/lambda-service/

WORKDIR /build/shared
RUN go mod download

WORKDIR /build/services/lambda-service
RUN go mod download

# Copy source
WORKDIR /build
COPY shared/ ./shared/
COPY services/lambda-service/ ./services/lambda-service/

# Build binary
WORKDIR /build/services/lambda-service
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a \
    -o lambda-service \
    ./cmd/main.go

# Runtime stage (Docker-in-Docker for container execution)
FROM docker:24-dind

# Install dependencies
RUN apk --no-cache add \
    ca-certificates \
    tzdata

WORKDIR /app

COPY --from=builder /build/services/lambda-service/lambda-service .

# Create necessary directories
RUN mkdir -p /app/functions /app/cache && \
    addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser -G docker appuser && \
    chown -R appuser:appuser /app

# Lambda service needs docker access
USER appuser

EXPOSE 8081

CMD ["./lambda-service"]
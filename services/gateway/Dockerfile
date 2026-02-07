# Build stage
FROM golang:1.25.7-alpine AS builder

WORKDIR /workspace

# Copy shared module manifests
COPY shared/go.mod shared/go.sum ./shared/

# Copy service manifests
COPY services/gateway/go.mod services/gateway/go.sum ./services/gateway/

# Download dependencies
WORKDIR /workspace/services/gateway
RUN go mod download

# Copy source code
COPY shared/ ../../shared/
COPY services/gateway/ .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o gateway ./cmd

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /workspace/services/gateway/gateway .

# Expose port
EXPOSE 8080

# Run
CMD ["./gateway"]

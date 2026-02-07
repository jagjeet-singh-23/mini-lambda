# Build stage
FROM golang:1.25.7-alpine AS builder

WORKDIR /workspace

# Copy shared module manifests
COPY shared/go.mod shared/go.sum ./shared/

# Copy service manifests
COPY services/lambda-service/go.mod services/lambda-service/go.sum ./services/lambda-service/

# Download dependencies
WORKDIR /workspace/services/lambda-service
RUN go mod download

# Copy source code
COPY shared/ ../../shared/
COPY services/lambda-service/ .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o lambda-service ./cmd

# Runtime stage
FROM docker:24-dind

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /workspace/services/lambda-service/lambda-service .

# Expose port
EXPOSE 8081

# Run
CMD ["./lambda-service"]

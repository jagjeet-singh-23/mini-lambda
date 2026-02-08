# Build stage
FROM golang:1.25.7-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

WORKDIR /build

# Copy go.mod files first for better caching
# This is KEY - layers rebuild only if go.mod/go.sum change
COPY shared/go.mod shared/go.sum ./shared/
COPY services/gateway/go.mod services/gateway/go.sum ./services/gateway/

# Download all dependencies (cached layer!)
WORKDIR /build/shared
RUN go mod download

WORKDIR /build/services/gateway
RUN go mod download

# Now copy source code (changes frequently)
WORKDIR /build
COPY shared/ ./shared/
COPY services/gateway/ ./services/gateway/

# Build the binary
WORKDIR /build/services/gateway
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a \
    -o gateway \
    ./cmd/main.go

# Runtime stage (minimal)
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy only the binary
COPY --from=builder /build/services/gateway/gateway .

# Non-root user for security
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser && \
    chown -R appuser:appuser /app

USER appuser

EXPOSE 8080

CMD ["./gateway"]

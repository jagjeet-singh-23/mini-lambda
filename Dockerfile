FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates curl docker-cli

# Create non-root user
RUN addgroup -g 1000 lambda && \
    adduser -D -u 1000 -G lambda lambda

WORKDIR /app

# Copy pre-built binary
COPY mini-lambda-linux ./mini-lambda
COPY migrations ./migrations

# Change ownership
RUN chown -R lambda:lambda /app

# Switch to non-root user
USER lambda

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Run the binary
CMD ["./mini-lambda"]
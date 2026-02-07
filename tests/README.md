# Mini-Lambda Testing Guide

## Testing Strategy

We follow a comprehensive testing approach with three levels:

1. **Unit Tests** - Test individual functions and methods
2. **Integration Tests** - Test service interactions with dependencies
3. **E2E Tests** - Test complete user workflows

## Directory Structure

```
tests/
├── unit/           # Unit tests for each service
│   ├── gateway/
│   ├── lambda-service/
│   └── build-service/
├── integration/    # Integration tests
│   ├── cache/
│   ├── database/
│   └── queue/
└── e2e/           # End-to-end tests
    ├── function_lifecycle/
    ├── invocation/
    └── build_pipeline/
```

## Running Tests

### Prerequisites

```bash
# Start infrastructure
cd infrastructure/docker
docker-compose up -d postgres redis rabbitmq minio

# Wait for services to be healthy
docker-compose ps
```

### Unit Tests

```bash
# Run all unit tests
make test-unit

# Run unit tests for specific service
cd services/gateway
go test ./... -v

cd services/lambda-service
go test ./... -v

cd services/build-service
go test ./... -v
```

### Integration Tests

```bash
# Run all integration tests (requires infrastructure)
make test-integration

# Run specific integration test
cd tests/integration
go test -v ./cache
go test -v ./database
go test -v ./queue
```

### E2E Tests

```bash
# Start all services
cd infrastructure/docker
docker-compose up -d

# Run E2E tests
make test-e2e

# Or run specific E2E test
cd tests/e2e
go test -v ./function_lifecycle
go test -v ./invocation
go test -v ./build_pipeline
```

### All Tests

```bash
# Run everything
make test-all
```

## Test Coverage

```bash
# Generate coverage report
make test-coverage

# View coverage in browser
go tool cover -html=coverage.out
```

## Writing Tests

### Unit Test Example

```go
// services/gateway/internal/ratelimit/token_bucket_test.go
package ratelimit_test

import (
    "context"
    "testing"
    "time"

    "github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/ratelimit"
)

func TestTokenBucket_Allow(t *testing.T) {
    // Use testcontainers for Redis
    ctx := context.Background()

    limiter, err := ratelimit.NewTokenBucketLimiter("localhost:6379", 10, 1)
    if err != nil {
        t.Fatalf("Failed to create limiter: %v", err)
    }
    defer limiter.Close()

    // Test: Should allow first request
    allowed, err := limiter.Allow(ctx, "test-key")
    if err != nil {
        t.Fatalf("Allow failed: %v", err)
    }
    if !allowed {
        t.Error("Expected first request to be allowed")
    }
}
```

### Integration Test Example

```go
// tests/integration/cache/redis_test.go
package cache_test

import (
    "context"
    "testing"
    "time"

    "github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/cache"
    "github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

func TestRedisCache_FunctionCaching(t *testing.T) {
    ctx := context.Background()

    redisCache, err := cache.NewRedisCache("localhost:6379", 5*time.Minute)
    if err != nil {
        t.Fatalf("Failed to create cache: %v", err)
    }
    defer redisCache.Close()

    // Test: Cache miss
    fn, err := redisCache.GetFunction(ctx, "non-existent")
    if err != nil {
        t.Fatalf("GetFunction failed: %v", err)
    }
    if fn != nil {
        t.Error("Expected cache miss")
    }

    // Test: Set and get
    testFn := &domain.Function{
        ID:   "test-123",
        Name: "test-function",
    }

    err = redisCache.SetFunction(ctx, testFn)
    if err != nil {
        t.Fatalf("SetFunction failed: %v", err)
    }

    cached, err := redisCache.GetFunction(ctx, "test-123")
    if err != nil {
        t.Fatalf("GetFunction failed: %v", err)
    }
    if cached == nil || cached.Name != "test-function" {
        t.Error("Expected function to be cached")
    }
}
```

### E2E Test Example

```go
// tests/e2e/function_lifecycle/create_invoke_test.go
package function_lifecycle_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "testing"
    "time"
)

func TestFunctionLifecycle_CreateAndInvoke(t *testing.T) {
    gatewayURL := "http://localhost:8080"

    // Step 1: Create function
    createReq := map[string]interface{}{
        "name":    "hello-world",
        "runtime": "python3.9",
        "handler": "index.handler",
        "package_data": "base64-encoded-zip",
        "webhook_url": "http://localhost:9999/webhook",
    }

    body, _ := json.Marshal(createReq)
    resp, err := http.Post(gatewayURL+"/functions", "application/json", bytes.NewReader(body))
    if err != nil {
        t.Fatalf("Create function failed: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusAccepted {
        t.Fatalf("Expected 202, got %d", resp.StatusCode)
    }

    var createResp map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&createResp)

    jobID := createResp["job_id"].(string)
    functionID := createResp["function_id"].(string)

    // Step 2: Wait for build to complete
    time.Sleep(5 * time.Second)

    // Step 3: Invoke function
    invokeReq := map[string]interface{}{
        "function_id": functionID,
        "payload": map[string]string{
            "message": "Hello, World!",
        },
    }

    body, _ = json.Marshal(invokeReq)
    resp, err = http.Post(gatewayURL+"/invoke", "application/json", bytes.NewReader(body))
    if err != nil {
        t.Fatalf("Invoke failed: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        t.Fatalf("Expected 200, got %d", resp.StatusCode)
    }

    t.Logf("Function lifecycle test passed! Job: %s, Function: %s", jobID, functionID)
}
```

## Continuous Integration

Tests are automatically run on:

- Every pull request
- Every commit to main branch
- Nightly builds

See `.github/workflows/test.yml` for CI configuration.

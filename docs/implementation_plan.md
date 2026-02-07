# Mini-Lambda Microservices Architecture - Implementation Roadmap

## 🎯 Goal

Transform monolithic Mini-Lambda into microservices architecture with:

- API Gateway (Rate limiting, routing)
- Build Service (Async, RabbitMQ-based)
- Lambda Service (Sync, high-performance execution)
- Redis caching layer
- Observability stack

## 🏗️ Architectural Decisions

### Database Strategy

**Split Databases per Service** (True Microservices)

- `build_service_db` - Build jobs, build status, package metadata
- `lambda_service_db` - Functions, invocations, cron jobs, event audit, DLQ

**Trade-offs:**

- ✅ True service isolation
- ✅ Independent scaling
- ❌ Need data sync for function metadata (handled via webhooks)

### Build Status Communication

**Webhook-based notifications** (not polling)

- Client provides webhook URL when creating function
- Build service POSTs to webhook on status changes: `queued → building → completed/failed`
- Deterministic endpoint pattern: `/webhooks/build-status/:job_id`

### Gateway Responsibilities

**Routing only, NOT service health management**

- Routes requests to appropriate service
- Returns `503 Service Unavailable` if downstream service is down (let client retry)
- Rate limiting: **Per function** (not per user)

### Build Service Implementation

**Phase 1: ZIP files only**

- Workers extract ZIP and upload to S3
- **Phase 2 (Future):** Dockerfile support via Docker CLI in worker containers

### Component Ownership

- **Lambda Service:** Execution, cron jobs, event audit, dead letter queues
- **Build Service:** Build orchestration, package processing
- **Gateway:** Routing, rate limiting, authentication

---

## 📁 New Project Structure

```
mini-lambda/
├── services/
│   ├── gateway/              # NEW - API Gateway
│   │   ├── cmd/
│   │   │   └── main.go
│   │   ├── internal/
│   │   │   ├── ratelimit/   # Redis-based rate limiting
│   │   │   ├── auth/        # JWT authentication
│   │   │   └── router/      # Request routing
│   │   ├── go.mod
│   │   └── Dockerfile
│   │
│   ├── build-service/        # NEW - Build Service
│   │   ├── cmd/
│   │   │   ├── master/      # Master node (job submission)
│   │   │   └── worker/      # Worker nodes (build execution)
│   │   ├── internal/
│   │   │   ├── builder/     # ZIP/Dockerfile builders
│   │   │   ├── storage/     # S3 operations
│   │   │   └── queue/       # RabbitMQ integration
│   │   ├── go.mod
│   │   └── Dockerfile
│   │
│   └── lambda-service/       # REFACTORED - Execution Service
│       ├── cmd/
│       │   └── main.go
│       ├── internal/
│       │   ├── executor/    # Function execution (from existing runtime/)
│       │   ├── cache/       # Redis cache layer
│       │   └── pool/        # Container pooling (from existing pool/)
│       ├── go.mod
│       └── Dockerfile
│
├── shared/                   # SHARED - Common code
│   ├── domain/              # Domain models (from existing internal/domain/)
│   ├── events/              # Event definitions
│   ├── metrics/             # Prometheus metrics
│   └── logger/              # Structured logging
│
├── infrastructure/           # Infrastructure configs
│   ├── docker/
│   │   ├── docker-compose.yml
│   │   └── docker-compose.prod.yml
│   ├── k8s/                 # Kubernetes manifests (future)
│   └── terraform/           # IaC (future)
│
└── observability/            # Monitoring stack
    ├── grafana/
    │   └── dashboards/
    ├── prometheus/
    │   └── prometheus.yml
    └── loki/
        └── loki-config.yml
```

---

## 🗺️ Phase-wise Implementation (6 Phases)

### Phase 0: Preparation (Day 1)

**Goal:** Setup new structure, no breaking changes

```bash
# Create new structure
mkdir -p services/{gateway,build-service,lambda-service}
mkdir -p shared/{domain,events,metrics,logger}
mkdir -p infrastructure/{docker,k8s,terraform}
mkdir -p observability/{grafana,prometheus,loki}
```

**Deliverables:**

- ✅ New directory structure
- ✅ Shared modules setup
- ✅ Docker compose for local development

---

### Phase 1: Extract Shared Code (Day 1-2)

**Goal:** Move common code to `shared/` module

**Steps:**

1. **Create shared module:**

```bash
cd shared
go mod init github.com/jagjeet-singh-23/mini-lambda/shared
```

2. **Move domain models:**

```bash
# Copy domain code
cp -r ../internal/domain ./
cp -r ../internal/logger ./
cp -r ../internal/metrics ./
```

3. **Update imports in existing code:**

```go
// Old
import "github.com/jagjeet-singh-23/mini-lambda/internal/domain"

// New
import "github.com/jagjeet-singh-23/mini-lambda/shared/domain"
```

**Testing:** Ensure existing service still compiles and runs

---

### Phase 2: Build Lambda Service (Day 2-3)

**Goal:** Extract execution logic into standalone service

**What to move from existing codebase:**

```
FROM internal/ TO services/lambda-service/internal/:
  ✅ runtime/       → executor/
  ✅ pool/          → pool/
  ✅ storage/s3.go  → storage/

KEEP in original:
  ❌ api/           (Will move to gateway)
  ❌ events/        (Will move to build-service)
```

**Implementation:**

```go
// services/lambda-service/cmd/main.go
package main

import (
    "log"
    "net/http"

    "github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/executor"
    "github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/cache"
    "github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

func main() {
    log.Println("🚀 Lambda Service starting...")

    // Initialize Redis cache
    redisCache := cache.NewRedisCache("localhost:6379")

    // Initialize executor (your existing runtime manager)
    executor := executor.NewExecutor(redisCache)

    // HTTP server (only /invoke endpoint)
    http.HandleFunc("/invoke", executor.HandleInvoke)
    http.HandleFunc("/health", healthCheck)

    log.Println("✅ Lambda Service listening on :8081")
    http.ListenAndServe(":8081", nil)
}
```

**Testing:** Lambda service runs independently

---

### Phase 3: Build Gateway Service (Day 3-4)

**Goal:** Create API Gateway with rate limiting

**Implementation:**

```go
// services/gateway/cmd/main.go
package main

import (
    "log"
    "net/http"

    "github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/ratelimit"
    "github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/router"
)

func main() {
    log.Println("🚀 API Gateway starting...")

    // Initialize Redis rate limiter
    rateLimiter := ratelimit.NewRedisRateLimiter("localhost:6379")

    // Initialize router
    r := router.NewRouter()

    // Add middleware
    r.Use(ratelimit.Middleware(rateLimiter))

    // Routes
    r.POST("/functions", r.ProxyToBuildService)
    r.POST("/functions/:id/invoke", r.ProxyToLambdaService)
    r.GET("/functions", r.ProxyToBuildService)

    log.Println("✅ Gateway listening on :8080")
    http.ListenAndServe(":8080", r)
}
```

**Testing:** Gateway routes requests correctly

---

### Phase 4: Build Service Implementation (Day 4-6)

**Goal:** Async build service with RabbitMQ

**Architecture:**

```
Master Node:
  - Receives build requests
  - Publishes to RabbitMQ
  - Returns 202 Accepted

Worker Nodes:
  - Consume from RabbitMQ
  - Build/Extract packages
  - Upload to S3
  - Update DB status
```

**Implementation:**

```go
// services/build-service/cmd/master/main.go
package main

func main() {
    log.Println("🚀 Build Service Master starting...")

    // Initialize RabbitMQ publisher
    publisher := queue.NewPublisher("amqp://localhost:5672")

    // HTTP server
    http.HandleFunc("/functions", handleCreateFunction)

    log.Println("✅ Build Master listening on :8082")
    http.ListenAndServe(":8082", nil)
}

func handleCreateFunction(w http.ResponseWriter, r *http.Request) {
    // Parse request
    var req CreateFunctionRequest
    json.NewDecoder(r.Body).Decode(&req)

    // Create build job
    job := BuildJob{
        FunctionID: uuid.New().String(),
        Runtime:    req.Runtime,
        PackageURL: uploadToS3(req.Package),
        WebhookURL: req.WebhookURL, // Client-provided webhook for status updates
    }

    // Publish to RabbitMQ
    publisher.Publish("build-jobs", job)

    // Return immediately
    w.WriteHeader(http.StatusAccepted)
    json.NewEncoder(w).Encode(map[string]string{
        "job_id": job.ID,
        "status": "queued",
    })
}
```

```go
// services/build-service/cmd/worker/main.go
package main

func main() {
    log.Println("🚀 Build Service Worker starting...")

    // Initialize RabbitMQ consumer
    consumer := queue.NewConsumer("amqp://localhost:5672")

    // Process jobs
    consumer.Consume("build-jobs", processBuildJob)
}

func processBuildJob(job BuildJob) error {
    log.Printf("Processing build job: %s", job.ID)

    // Notify: building started
    notifyWebhook(job.WebhookURL, "building", job.ID)

    // 1. Download package from S3
    pkg := downloadFromS3(job.PackageURL)

    // 2. Build (Phase 1: ZIP only, Phase 2: Dockerfile via Docker CLI)
    if isZIP(pkg) {
        extractZIP(pkg)
    } else {
        // Future: Dockerfile support
        // buildDockerImage(pkg) using Docker CLI in worker container
        notifyWebhook(job.WebhookURL, "failed", job.ID)
        return fmt.Errorf("Dockerfile support not yet implemented")
    }

    // 3. Upload artifacts to S3
    uploadToS3(builtArtifacts)

    // 4. Update status in build_service_db
    updateBuildStatus(job.ID, "completed")

    // 5. Notify webhook: completed
    notifyWebhook(job.WebhookURL, "completed", job.ID)

    return nil
}

func notifyWebhook(webhookURL, status, jobID string) {
    payload := map[string]string{
        "job_id": jobID,
        "status": status,
        "timestamp": time.Now().Format(time.RFC3339),
    }
    // POST to client's webhook endpoint
    http.Post(webhookURL, "application/json", marshalJSON(payload))
}
```

**Testing:** Build jobs process successfully

---

### Phase 5: Add Redis Caching (Day 6-7) ✅ COMPLETED

**Goal:** Cache function metadata to reduce DB load

**Implementation:**

- Created Redis cache package with TTL support
- Implemented `CachedFunctionRepository` using cache-aside pattern
- Integrated Redis into Lambda Service
- Added Redis to docker-compose infrastructure

**Testing:**

- Validated cache hits/misses with integration tests
- Verified TTL expiration

---

### Phase 5.5: Testing Infrastructure (Added) ✅ COMPLETED

**Goal:** Establish comprehensive testing setup

**Deliverables:**

- ✅ Fixed integration test package visibility issues (moved to `internal/`)
- ✅ Fixed Docker build context to include shared module
- ✅ Created `tests/README.md` and `README_MICROSERVICES.md`
- ✅ Initialized `Makefile` with test commands

---

### Phase 6: Observability Stack (Day 7-8)

**Goal:** Centralized logging and metrics

**1. Infrastructure (docker-compose.yml)**

- **Prometheus:** Metrics collection (scrape interval: 15s)
- **Loki:** Log aggregation
- **Promtail:** Log shipping (Docker logs -> Loki)
- **Grafana:** Visualization

**2. Application Instrumentation (Go Code)**

- Add `prometheus` middleware to all services
- Expose `/metrics` endpoint on all services
- Structured logging (JSON) for Loki ingestion

**3. Configuration**

- `prometheus.yml`: Scrape configs for gateway, lambda, build services
- `loki-config.yml`: Retention and storage
- `promtail-config.yml`: Docker socket binding

**4. Grafana Dashboards**

- **System Overview:** CPU, Memory, Goroutines
- **Request Traffic:** RPS per service, Latency (p95, p99)
- **Build Metrics:** Queue depth, Build duration, Failure rate
- **Lambda Metrics:** Invocation duration, Cold start rate

**Testing:**

- Verify `/metrics` endpoints
- Query logs in Grafana Explore
- Visualize data in Dashboards

---

## 📋 Changes to Existing Codebase

### Files to MOVE:

```bash
# Move to shared/
internal/domain/          → shared/domain/
internal/logger/          → shared/logger/
internal/metrics/         → shared/metrics/

# Move to lambda-service/
internal/runtime/         → services/lambda-service/internal/executor/
internal/pool/            → services/lambda-service/internal/pool/
internal/events/          → services/lambda-service/internal/events/
internal/cron/            → services/lambda-service/internal/cron/
internal/storage/s3.go    → services/lambda-service/internal/storage/s3.go

# Move to build-service/
# (Build service will have its own S3 client and DB client)
# No files from existing codebase move here - new implementation
```

### Database Split:

```bash
# Current: single postgres.go
internal/storage/postgres.go

# After split:
services/lambda-service/internal/storage/postgres.go
  - Tables: functions, invocations, cron_jobs, event_audit, dead_letter_queue

services/build-service/internal/storage/postgres.go
  - Tables: build_jobs, build_status, package_metadata
```

### Files to DELETE (after migration):

```bash
# Will be replaced by gateway
internal/api/handler.go
internal/api/async_handler.go
internal/api/router.go

# Will be replaced by separate services
cmd/server/main.go
```

### Files to KEEP (unchanged):

```bash
# Config remains
internal/config/
```

---

## 🎯 Migration Strategy

### Option 1: Big Bang (Risky)

```
Day 1-8: Build everything
Day 9:   Switch completely
```

❌ High risk, no rollback

### Option 2: Strangler Pattern (RECOMMENDED) ✅

```
Phase 1: Run old + new in parallel
Phase 2: Route 10% traffic to new
Phase 3: Route 50% traffic to new
Phase 4: Route 100% traffic to new
Phase 5: Deprecate old
```

✅ Safe, gradual migration

**Implementation:**

```
Load Balancer
    ↓
┌───────┴────────┐
│                │
Old Service   New Services
(90%)         (10%)
    ↓              ↓
Shared Database
```

---

## 🚀 Getting Started (Today!)

### Step 1: Create new structure

```bash
cd mini-lambda
mkdir -p services/{gateway,build-service,lambda-service}
mkdir -p shared/{domain,events,metrics,logger}
```

### Step 2: Initialize modules

```bash
# Shared module
cd shared
go mod init github.com/jagjeet-singh-23/mini-lambda/shared

# Gateway
cd ../services/gateway
go mod init github.com/jagjeet-singh-23/mini-lambda/services/gateway

# Build service
cd ../build-service
go mod init github.com/jagjeet-singh-23/mini-lambda/services/build-service

# Lambda service
cd ../lambda-service
go mod init github.com/jagjeet-singh-23/mini-lambda/services/lambda-service
```

### Step 3: Copy domain code

```bash
cp -r ../../internal/domain ../shared/
cp -r ../../internal/logger ../shared/
```

### Step 4: Test existing service still works

```bash
cd ../..
go run cmd/server/main.go
# Should still work!
```

---

**Kya bolte ho? Phase 1 se start karein? 🚀**

# Mini-Lambda Microservices - Quick Start Guide

## 🚀 Overview

Mini-Lambda is a serverless function execution platform built with microservices architecture.

### Architecture

```
┌─────────────┐
│   Gateway   │ :8080 (Rate Limiting, Routing)
└──────┬──────┘
       │
       ├──────────────┬──────────────┐
       │              │              │
┌──────▼──────┐ ┌────▼─────┐ ┌──────▼────────┐
│Lambda Service│ │Build     │ │Build Workers  │
│    :8081     │ │Master    │ │  (N replicas) │
│              │ │  :8082   │ │               │
└──────┬───────┘ └────┬─────┘ └───────┬───────┘
       │              │               │
       └──────────────┴───────────────┘
                      │
       ┌──────────────┴──────────────┐
       │                             │
┌──────▼──────┐  ┌────────┐  ┌──────▼──────┐
│  Postgres   │  │ Redis  │  │  RabbitMQ   │
│    :5432    │  │ :6379  │  │    :5672    │
└─────────────┘  └────────┘  └─────────────┘
       │
┌──────▼──────┐
│   MinIO     │
│  :9000      │
└─────────────┘
```

## 📦 Prerequisites

- Docker & Docker Compose
- Go 1.25.7+
- Make

## 🏃 Quick Start

### 1. Start Infrastructure Only

```bash
make docker-up-infra
```

This starts:

- PostgreSQL (with lambda_service_db and build_service_db)
- Redis
- RabbitMQ
- MinIO

### 2. Build All Services

```bash
make build-services
```

### 3. Start All Services

```bash
make docker-up-all
```

### 4. Verify Services

```bash
# Check health
curl http://localhost:8080/health  # Gateway
curl http://localhost:8081/health  # Lambda Service
curl http://localhost:8082/health  # Build Master

# View logs
make docker-logs
```

## 🧪 Testing

### Unit Tests (No Dependencies)

```bash
make test-unit
```

### Integration Tests (Requires Infrastructure)

```bash
make test-integration
```

### E2E Tests (Requires All Services)

```bash
make test-e2e
```

### All Tests

```bash
make test-all
```

### Coverage Report

```bash
make test-coverage
```

## 🔧 Development

### Run Services Locally

```bash
# Terminal 1: Infrastructure
make docker-up-infra

# Terminal 2: Gateway
cd services/gateway
go run ./cmd

# Terminal 3: Lambda Service
cd services/lambda-service
go run ./cmd

# Terminal 4: Build Master
cd services/build-service
go run ./cmd/master

# Terminal 5: Build Worker
cd services/build-service
go run ./cmd/worker
```

## 📊 Monitoring

### RabbitMQ Management UI

- URL: http://localhost:15672
- Username: `guest`
- Password: `guest`

### MinIO Console

- URL: http://localhost:9001
- Username: `minioadmin`
- Password: `minioadmin`

## 🌐 API Endpoints

### Gateway (Port 8080)

```bash
# Create Function
POST /functions
{
  "name": "hello-world",
  "runtime": "python3.9",
  "handler": "index.handler",
  "package_data": "<base64-encoded-zip>",
  "webhook_url": "http://your-webhook.com/callback"
}

# Invoke Function
POST /invoke
{
  "function_id": "uuid",
  "payload": {"key": "value"}
}

# Get Function
GET /functions?id=uuid

# List Functions
GET /functions

# Health Check
GET /health
```

## 🗄️ Database Schema

### lambda_service_db

- `functions` - Function metadata
- `invocations` - Execution history
- `cron_jobs` - Scheduled jobs
- `webhooks` - Webhook configurations
- `event_audit` - Event audit log
- `dead_letter_queue` - Failed events

### build_service_db

- `build_jobs` - Build job queue
- `build_metadata` - Build statistics

## 🛠️ Useful Commands

```bash
# View all available commands
make help

# Clean everything
make clean-services
make docker-clean

# Format code
make fmt

# View logs
make docker-logs

# Stop all services
make docker-down
```

## 🐛 Troubleshooting

### Services won't start

```bash
# Check Docker
docker ps

# Check logs
make docker-logs

# Clean and restart
make docker-clean
make docker-up-all
```

### Database connection issues

```bash
# Check Postgres is healthy
docker ps | grep postgres

# Connect to database
docker exec -it mini-lambda-postgres psql -U postgres -d lambda_service_db
```

### Redis connection issues

```bash
# Check Redis
docker exec -it mini-lambda-redis redis-cli ping
```

## 📚 Documentation

- [Testing Guide](tests/README.md)
- [Implementation Plan](docs/implementation_plan.md)

## 🎯 Next Steps

1. ✅ All core microservices built
2. ✅ Redis caching implemented
3. ⏳ Phase 6: Observability (Prometheus, Loki, Grafana)
4. ⏳ Function versioning & traffic routing
5. ⏳ Auto-scaling workers

## 📝 License

MIT

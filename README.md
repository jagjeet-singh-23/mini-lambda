# Mini-Lambda

A highly scalable, Kubernetes-native Serverless Function-as-a-Service (FaaS) Platform engineered in Go.

## Overview

**Mini-Lambda** is a production-grade backend architecture built from scratch to reverse-engineer and implement the core mechanics of cloud serverless platforms (like AWS Lambda).

It orchestrates dynamic code execution utilizing a robust API Gateway, an asynchronous RabbitMQ build pipeline, distributed Redis rate limiting, and an elastic execution pool managed natively by Kubernetes Horizontal Pod Autoscaling (HPA).

## 🏗 System Architecture Features

- **Custom API Gateway:** Acts as the ingress controller, handling dynamic routing, distributed tracing, and rich middleware.
- **Resilient Traffic Control:** Implemented the Token Bucket algorithm using atomic **Redis Lua Scripts** for distributed rate limiting.
- **Circuit Breakers (State Pattern):** Dynamically isolates downstream microservices. If the Build Service goes down, the Lambda Execution API remains instantly available without cascading failures.
- **Asynchronous Build Pipeline:** Inbound zip functions are hashed for idempotency and uploaded to **MinIO (S3)**. Build jobs are dispatched to **RabbitMQ**, where decoupled worker nodes safely compile the runtimes.
- **Elastic Invocation Engine:** Incoming `/invoke` payloads target an internal runner pool natively managed by the **Kubernetes HPA** (scaling dynamically on CPU/Memory pressure).
- **Comprehensive Observability:** Fully instrumented edge-to-edge with **Prometheus**, **Loki Logs**, **Grafana Dashboards**.

## �🗂 Project Structure

```text
├── cmd/                # Entrypoints for microservices
├── services/
│   ├── gateway/        # API Ingress, Routing, Circuit Breakers, Rate Limiters
│   ├── build-service/  # Idempotency checks, MinIO uploads, RabbitMQ publisher
│   └── lambda-service/ # Execution engine, pool management, Docker/K8s runtime
├── shared/             # Common models, logger, tracing middlewares
└── infrastructure/
    ├── k8s/manifests/  # YAML definitions for Deployments, HPA, ConfigMaps
    └── docker/runners/ # Wrapper Dockerfiles (Python/Nodejs) for lambda execution
```

## 🚀 Quick Start deployment (Kubernetes / Kind)

### Prerequisites

- Go 1.21+
- Docker Desktop
- `kind` (Kubernetes in Docker)
- `kubectl`

### 1. Spin up the Local Cluster

```bash
kind create cluster --config infrastructure/k8s/kind-config.yaml
```

### 2. Deploy Infrastructure Dependencies

Deploy Postgres, Redis (split cache and ratelimit arrays), RabbitMQ, and MinIO:

```bash
kubectl apply -f infrastructure/k8s/manifests/postgres.yaml
# ... (apply remaining databases from manifests directory)
```

### 3. Deploy Observability Stack (Optional)

```bash
kubectl apply -f infrastructure/k8s/manifests/observability/
```

### 4. Deploy Mini-Lambda Core Services

```bash
kubectl apply -f infrastructure/k8s/manifests/configmap.yaml
kubectl apply -f infrastructure/k8s/manifests/gateway.yaml
kubectl apply -f infrastructure/k8s/manifests/build-master.yaml
kubectl apply -f infrastructure/k8s/manifests/build-worker.yaml
kubectl apply -f infrastructure/k8s/manifests/lambda-service.yaml
```

## 🛠 API Usage

### 1. Create a Serverless Function

```bash
curl -X POST http://localhost:8080/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name": "helloworld",
    "runtime": "python3.11",
    "package_data": "<BASE64_ZIPPED_PYTHON_CODE>"
  }'
```

_Returns HTTP 202 Accepted with a unique `job_id` and `function_id` pending asynchronous build._

### 2. Invoke the Function

```bash
curl -X POST http://localhost:8080/invoke \
  -H "Content-Type: application/json" \
  -d '{
    "function_id": "<RETURNED_FUNCTION_ID>",
    "payload": {"name": "World"}
  }'
```

---

# Production Deployment & CD Guide

This guide describes how to set up **Auto-Deployment (Continuous Deployment)** and manage your production infrastructure for the `mini-lambda` project using either **Docker Compose** or **Kubernetes (Kind)**.

## 1. Setup Environment Variables

On your production server, create a `.env` file in the project directory:

```bash
cp .env.production.example .env
nano .env
```

Ensure `GITHUB_USER` is set correctly (e.g., `jagjeet-singh-23`) and provide secure passwords for all services.

## 2. Set up Auto-Deployment (SSH)

To enable automatic deployment on every push to `main`, follow these steps:

### A. Generate SSH Key Pair

On your local machine or server, generate a new SSH key:

```bash
ssh-keygen -t ed25519 -C "github-actions-deploy"
```

- **Private Key**: `~/.ssh/id_ed25519`
- **Public Key**: `~/.ssh/id_ed25519.pub`

### B. Add Public Key to Server

Append the content of `id_ed25519.pub` to `~/.ssh/authorized_keys` on your production server.

### C. Add GitHub Secrets

In your GitHub repository, go to **Settings > Secrets and variables > Actions** and add the following secrets:

| Secret Name       | Description                                              |
| :---------------- | :------------------------------------------------------- |
| `SSH_HOST`        | The IP address or domain of your server                  |
| `SSH_USER`        | The SSH username (e.g., `root` or `ubuntu`)              |
| `SSH_PRIVATE_KEY` | The **entire content** of the private key (`id_ed25519`) |

## 3. GitHub Actions CI/CD (Recommended)

The easiest way to deploy is to push changes to the `stage` branch. The included GitHub Action (`.github/workflows/deploy.yml`) will automatically:

1. Build all docker images
2. Push them to GitHub Container Registry (GHCR)
3. Spin up a local `kind` Kubernetes cluster (if not already running)
4. Deploy the infrastructure manifests
5. Execute the database migration job (`db-migrator-job`)
6. Perform a rollout restart on the application deployments

## 4. Kubernetes Deployment Commands (Manual)

If you prefer deploying manually via Kubernetes without CI/CD, you can use the `kind` cluster setup.

### Setting up the Kind Cluster

```bash
# 1. Create the kind cluster with required port mappings
kind create cluster --config infrastructure/k8s/kind-config.yaml

# 2. Deploy namespaces and configuration
kubectl apply -f infrastructure/k8s/manifests/namespace.yaml
kubectl apply -f infrastructure/k8s/manifests/configmap.yaml
kubectl apply -f infrastructure/k8s/manifests/secrets.yaml

# 3. Deploy Databases and Storage
kubectl apply -f infrastructure/k8s/manifests/postgres.yaml
kubectl apply -f infrastructure/k8s/manifests/redis.yaml
kubectl apply -f infrastructure/k8s/manifests/rabbitmq.yaml
kubectl apply -f infrastructure/k8s/manifests/minio.yaml

# 4. Deploy Application Services
kubectl apply -f infrastructure/k8s/manifests/gateway.yaml
kubectl apply -f infrastructure/k8s/manifests/build-master.yaml
kubectl apply -f infrastructure/k8s/manifests/build-worker.yaml
kubectl apply -f infrastructure/k8s/manifests/lambda-service.yaml

# 5. Run Database Migrations
kubectl delete job db-migrator-job -n mini-lambda --ignore-not-found
kubectl apply -f infrastructure/k8s/manifests/db-migrator.yaml

# 6. Deploy Observability Stack
kubectl apply -f infrastructure/k8s/manifests/observability/
```

### Scaling Services in Kubernetes

To run multiple build-workers in K8s:

```bash
kubectl scale deployment build-worker -n mini-lambda --replicas=3
```

## 5. Exposing & Accessing Services Locally

Because this runs inside `kind`, we've exposed specific NodePorts to your local host machine:

- **API Gateway**: `http://localhost:8080` (Port 30080 -> 8080)
- **MinIO API/S3 Endpoint**: `http://localhost:9000` (Port 30090 -> 9000)
- **MinIO Console (UI)**: `http://localhost:9001` (Port 30091 -> 9001, Login: minioadmin/minioadmin)
- **Grafana Dashboards**: `http://localhost:3000` (Port 30000 -> 3000, Login: admin/admin)

To verify the endpoints, run:

```bash
docker ps
```

You should see port forwardings attached to the `kind-control-plane` container.

## 6. Troubleshooting & Monitoring

- **Check Pod Status**: `kubectl get pods -n mini-lambda`
- **Check Specific Pod Logs**: `kubectl logs deploy/gateway -n mini-lambda`
- **Check Migration Logs**: `kubectl logs job/db-migrator-job -n mini-lambda`
- **Check Load/Resource Usage**: `kubectl top pods -n mini-lambda` (Requires Metrics Server)

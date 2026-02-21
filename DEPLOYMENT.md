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

## 3. Deployment Commands

### Manual Start

```bash
docker compose -f docker-compose.production.yml up -d
```

### Scaling Workers

To run multiple build-workers (e.g., 3 workers):

```bash
docker compose -f docker-compose.production.yml up -d --scale build-worker=3
```

### Pulling Latest Images (Manual)

```bash
docker compose -f docker-compose.production.yml pull
docker compose -f docker-compose.production.yml up -d
```

## 4. Kubernetes Deployment Commands

If you prefer deploying via Kubernetes instead of Docker Compose, an infrastructure setup is provided using `kind` (Kubernetes in Docker).

### Setting up the Kind Cluster

```bash
# 1. Create the kind cluster with required port mappings
kind create cluster --config infrastructure/k8s/kind-config.yaml

# 2. Deploy namespaces and configuration
kubectl apply -f infrastructure/k8s/manifests/namespace.yaml
kubectl apply -f infrastructure/k8s/manifests/configmap.yaml
kubectl apply -f infrastructure/k8s/manifests/secrets.yaml

# 3. Deploy Databases and Message Brokers
kubectl apply -f infrastructure/k8s/manifests/postgres.yaml
kubectl apply -f infrastructure/k8s/manifests/redis.yaml
kubectl apply -f infrastructure/k8s/manifests/rabbitmq.yaml
kubectl apply -f infrastructure/k8s/manifests/minio.yaml

# 4. Deploy Application Services
kubectl apply -f infrastructure/k8s/manifests/gateway.yaml
kubectl apply -f infrastructure/k8s/manifests/build-master.yaml
kubectl apply -f infrastructure/k8s/manifests/build-worker.yaml
kubectl apply -f infrastructure/k8s/manifests/lambda-service.yaml

# 5. (Optional) Deploy Observability Stack
kubectl apply -f infrastructure/k8s/manifests/observability/
```

### Scaling Workers in Kubernetes

To run multiple build-workers in K8s:

```bash
kubectl scale deployment build-worker -n mini-lambda --replicas=3
```

## 5. Troubleshooting & Monitoring

- **Check Logs**: `docker compose -f docker-compose.production.yml logs -f`
- **Check Status**: `docker compose -f docker-compose.production.yml ps`
- **Check Stats**: `docker stats`
- **Observability**:
  - **Grafana**: `http://<server-ip>:3000` (Default: admin/admin)
  - **Gateway**: `http://<server-ip>:8080`

### Image Pruning

The auto-deploy script runs `docker image prune -f` to clean up old images and save disk space.

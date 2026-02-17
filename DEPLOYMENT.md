# Deployment Guide

This guide describes how to deploy the `mini-lambda` project on a remote server using Docker Compose and images from GHCR.

## Prerequisites

- A remote server with Docker and Docker Compose installed.
- Access to the GitHub Container Registry (GHCR).
- A personal access token (PAT) with `read:packages` permissions.

## Step-by-Step Deployment

### 1. Set up Environment Variables

Create a `.env` file on your server or export these variables in your shell:

```bash
export GITHUB_USER="your-github-username"
export POSTGRES_PASSWORD="secure-password"
export RABBITMQ_USER="admin"
export RABBITMQ_PASS="secure-password"
export MINIO_ROOT_USER="admin"
export MINIO_ROOT_PASSWORD="secure-password"
export GRAFANA_USER="admin"
export GRAFANA_PASS="secure-password"
```

### 2. Log in to GHCR

```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u $GITHUB_USER --password-stdin
```

### 3. Pull the Latest Images

```bash
# Set variables if not using .env
GITHUB_USER=frustrated-dev docker compose -f docker-compose.production.yml pull
```

### 4. Start the Application

```bash
GITHUB_USER=frustrated-dev docker compose -f docker-compose.production.yml up -d
```

### 5. Verify the Deployment

Check the status of the containers:

```bash
docker compose -f docker-compose.production.yml ps
```

You should see all services running. The Gateway will be available on port 80, and Grafana on port 3000.

## CI/CD Pipeline

The project includes a GitHub Actions workflow that automatically:

1. Builds Docker images for all services.
2. Tags them with `latest` and the commit SHA.
3. Pushes them to GHCR.

This pipeline triggers on every push to the `main` branch.

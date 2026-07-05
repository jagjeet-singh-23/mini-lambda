#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"

BUCKET="mini-lambda"

echo "==> Starting infrastructure services..."
docker compose up -d --build postgres redis-cache rabbitmq minio db-migrator git-fixture

echo "==> Waiting for MinIO healthcheck..."
until [ "$(docker inspect -f '{{.State.Health.Status}}' mini-lambda-minio 2>/dev/null)" = "healthy" ]; do
  sleep 2
done

echo "==> Ensuring S3 bucket '${BUCKET}' exists..."
docker run --rm --add-host=host.docker.internal:host-gateway --entrypoint sh minio/mc -c \
  "mc alias set local http://host.docker.internal:9000 minioadmin minioadmin >/dev/null && mc mb --ignore-existing local/${BUCKET}"

echo "==> Starting application services..."
docker compose up -d --build gateway build-master build-worker lambda-service

echo "==> Waiting for gateway to report healthy..."
until curl -sf http://localhost:8080/health >/dev/null 2>&1; do
  sleep 2
done

echo "==> Running e2e suite..."
cd "$REPO_ROOT/tests/e2e"
go test ./... -v -run . -count=1

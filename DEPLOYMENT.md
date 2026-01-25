# Production Deployment Guide

## Architecture Overview

```
                    Internet
                        ↓
              [Nginx Load Balancer]
               (Port 8080 → 80)
                        ↓
        ┌───────────────┼───────────────┐
        ↓               ↓               ↓
  [mini-lambda-1] [mini-lambda-2] [mini-lambda-3]
   (Instance 1)    (Instance 2)    (Instance 3)
        ↓               ↓               ↓
        └───────────────┴───────────────┘
                        ↓
        ┌───────────────┴───────────────┐
        ↓                               ↓
   [PostgreSQL]                     [MinIO]
   (Shared DB)                   (Shared Storage)
```

## Services

| Service | Port | Purpose |
|---------|------|---------|
| Nginx | 8080 | Load balancer (external access) |
| mini-lambda-1 | 8080 (internal) | Worker instance 1 |
| mini-lambda-2 | 8080 (internal) | Worker instance 2 |
| mini-lambda-3 | 8080 (internal) | Worker instance 3 |
| PostgreSQL | 5432 | Shared database |
| MinIO | 9000, 9001 | Shared S3 storage |
| Prometheus | 9090 | Metrics collection (all 3 instances) |
| Grafana | 3000 | Metrics visualization |

## Deployment

### 1. Build and Start All Services

```bash
# Build the mini-lambda image and start all services
docker-compose up -d --build

# This will start:
# - 3 mini-lambda instances
# - 1 Nginx load balancer
# - PostgreSQL, MinIO, Prometheus, Grafana
```

### 2. Apply Database Migration

```bash
# Run migration (only needs to be done once)
make migrate-up
```

### 3. Verify Deployment

```bash
# Check all services are running
docker-compose ps

# Expected output: 8 containers running
# - mini-lambda-nginx
# - mini-lambda-1, mini-lambda-2, mini-lambda-3
# - postgres, minio, prometheus, grafana
```

### 4. Test Load Balancing

```bash
# Health check - should rotate through instances
for i in {1..6}; do
  curl -s http://localhost:8080/health | jq '.instance_id'
done

# Expected output (round-robin):
# "1"
# "2"
# "3"
# "1"
# "2"
# "3"
```

## Load Balancing Strategy

**Nginx uses `least_conn`** (least connections):
- Requests go to the instance with fewest active connections
- Better than round-robin for varying execution times
- Automatically handles instance failures

## Scaling

### Add More Instances

1. Edit `docker-compose.yml`:
```yaml
mini-lambda-4:
  # Copy mini-lambda-3 config
  # Change INSTANCE_ID to 4
```

2. Edit `nginx.conf`:
```nginx
upstream mini_lambda_backend {
    server mini-lambda-4:8080 max_fails=3 fail_timeout=30s;
}
```

3. Edit `monitoring/prometheus.yml`:
```yaml
- targets: 
    - 'mini-lambda-4:8080'
```

4. Restart:
```bash
docker-compose up -d --build
```

### Remove Instances

Simply comment out or remove the instance from docker-compose and nginx.conf.

## Monitoring

### Check Instance Health

```bash
# Via Nginx (load balanced)
curl http://localhost:8080/health | jq

# Direct to specific instance
docker exec mini-lambda-1 wget -qO- http://localhost:8080/health | jq
```

### View Metrics

```bash
# Prometheus (all instances)
open http://localhost:9090

# Query: mini_lambda_pool_size
# You'll see metrics from all 3 instances

# Grafana dashboard
open http://localhost:3000
# Login: admin/admin
```

### Check Nginx Logs

```bash
# Access logs (see which instance handled each request)
docker logs mini-lambda-nginx

# Error logs
docker exec mini-lambda-nginx cat /var/log/nginx/mini-lambda-error.log
```

## Performance Testing

### Load Test with Multiple Instances

```bash
# Run benchmark (will be distributed across 3 instances)
./benchmark.sh

# Expected throughput: ~3x single instance
# - Single instance: ~30-50 req/sec
# - 3 instances: ~90-150 req/sec
```

### Monitor Distribution

```bash
# Check pool stats per instance
curl http://localhost:8080/stats/pools | jq

# The load should be roughly evenly distributed
```

## Troubleshooting

### Instance Not Responding

```bash
# Check instance logs
docker logs mini-lambda-1

# Restart specific instance
docker-compose restart mini-lambda-1

# Nginx will automatically route around failed instances
```

### Uneven Load Distribution

```bash
# Check Nginx upstream status
docker exec mini-lambda-nginx cat /var/log/nginx/mini-lambda-access.log | tail -20

# Verify least_conn is working
# Requests should go to least busy instance
```

### Database Connection Issues

```bash
# All instances share the same PostgreSQL
# Check connection pool isn't exhausted
docker logs mini-lambda-postgres
```

## Production Considerations

### 1. **Resource Limits**

Add to docker-compose.yml:
```yaml
mini-lambda-1:
  deploy:
    resources:
      limits:
        cpus: '2'
        memory: 4G
      reservations:
        cpus: '1'
        memory: 2G
```

### 2. **Health Checks**

Already configured in Nginx:
- `max_fails=3` - Mark instance down after 3 failures
- `fail_timeout=30s` - Retry after 30 seconds

### 3. **Persistent Volumes**

Data persists in Docker volumes:
- `postgres_data` - Database
- `minio_data` - S3 objects
- `prometheus_data` - Metrics history
- `grafana_data` - Dashboards

### 4. **Backup Strategy**

```bash
# Backup database
docker exec mini-lambda-postgres pg_dump -U postgres mini_lambda > backup.sql

# Backup MinIO data
docker exec mini-lambda-minio mc mirror /data ./minio-backup
```

## Summary

✅ **3 Mini-Lambda Instances** - Horizontal scaling  
✅ **Nginx Load Balancer** - Least connections strategy  
✅ **Shared State** - PostgreSQL + MinIO  
✅ **Full Observability** - Prometheus + Grafana  
✅ **Production Ready** - Health checks, auto-restart, logging  

Your mini-lambda is now production-grade and AWS Lambda-like! 🚀

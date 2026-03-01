#!/bin/bash
set -e

# Ensure we are at project root relative to script
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$DIR/../../"

echo "=========================================================="
echo " 🚀 Complete AWS Infrastructure Deployment (Mini-Lambda)"
echo "=========================================================="

echo "🚀 Step 1: Provisioning EKS, ALB Controller, and Managed Services..."
# This handles the eksctl VPC/Cluster build, Helm ALB deployment, and Terraform RDS/Redis
./infrastructure/eks/setup.sh

echo ""
echo "🚀 Step 2: Deploying Kubernetes Manifests (Gateway, Lambda, RMQ, etc)..."
# This dynamically injects the .env settings and creates the deployments in the cluster
./infrastructure/eks/deploy.sh

echo "=========================================================="
echo "✅ All AWS Resources successfully brought back online!"
echo "Note: To run the 100K RPS load tests again on EC2, execute:"
echo " ./infrastructure/load-testing/multi_tenant_test.sh"
echo "=========================================================="

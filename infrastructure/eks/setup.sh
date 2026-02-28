#!/bin/bash
set -e

# Ensure we are at project root relative to script
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$DIR/../../"

echo "=========================================================="
echo " AWS Infrastructure Provisioning: Phase 9 Complete Pipeline "
echo "=========================================================="

echo "🚀 [1/3] Provisioning EKS Cluster with eksctl (approx. 15-20 mins)..."
eksctl create cluster -f infrastructure/eks/cluster.yaml

echo "🚀 [2/3] Installing AWS Load Balancer Controller via Helm..."
helm repo add eks https://aws.github.io/eks-charts
helm repo update eks
helm upgrade -i aws-load-balancer-controller eks/aws-load-balancer-controller \
  -n kube-system \
  --set clusterName=mini-lambda-prod \
  --set serviceAccount.create=false \
  --set serviceAccount.name=aws-load-balancer-controller

echo "🚀 [3/3] Provisioning Managed Services (RDS & ElastiCache) via Terraform..."
cd infrastructure/eks
terraform init
terraform apply -auto-approve

echo "=========================================================="
echo "✅ AWS Infrastructure successfully deployed!"
echo "Next steps:"
echo "1. Update your k8s ConfigMaps with the RDS and Redis endpoints outputted above."
echo "2. Deploy the Mini-Lambda application logic into EKS."
echo "=========================================================="

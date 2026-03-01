#!/bin/bash
set -e
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$DIR/../../"

echo "=========================================================="
echo " 🧨 AWS Infrastructure Teardown (Mini-Lambda)"
echo "=========================================================="
echo "This script will completely destroy your EC2 load generators, EKS Cluster, ALB, RDS, ElastiCache, and internal K8s workloads (RabbitMQ/Postgres)."
echo "Press Ctrl+C immediately if this is a mistake. Continuing in 5 seconds..."
sleep 5

echo ""
echo "🧨 [1/4] Destroying EC2 Load Generators (us-east-1)..."
cd infrastructure/load-testing
terraform destroy -auto-approve || echo "⚠️ Terraform load-testing destroy had issues or was already destroyed."
cd ../../

echo ""
echo "🧨 [2/4] Deleting Kubernetes namespace (removes ALB, RMQ, Postgres, Pods)..."
# Important to delete namespace first so ALB aws-load-balancer-controller cleanly un-provisions the ALB before cluster dies.
kubectl delete namespace mini-lambda --ignore-not-found || true
echo "Waiting a few moments for AWS resources to detach..."
sleep 15

echo ""
echo "🧨 [3/4] Destroying Managed Services (ElastiCache, S3) in ap-south-1..."
cd infrastructure/eks
terraform destroy -auto-approve || echo "⚠️ Terraform managed-services destroy had issues."
cd ../../

echo ""
echo "🧨 [4/4] Destroying EKS Cluster (VPC, Nodes, Control Plane)..."
# This cleanly destroys the NAT Gateways, VPC, and EC2 Node Groups mapped to the cluster
eksctl delete cluster -f infrastructure/eks/cluster.yaml --wait

echo "=========================================================="
echo "✅ All AWS Resources Successfully Torn Down!"
echo "Enjoy the cost savings!"
echo "=========================================================="

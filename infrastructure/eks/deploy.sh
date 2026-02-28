#!/bin/bash
set -e

# Ensure we are at relative path
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$DIR/../../"

# Load environment variables from .env natively
if [ -f "$DIR/../../.env" ]; then
    set -a
    source "$DIR/../../.env"
    set +a
    
    # CRITICAL: Unset AWS dummy credentials from .env so they don't break kubectl EKS authentication!
    unset AWS_ACCESS_KEY_ID
    unset AWS_SECRET_ACCESS_KEY
    unset AWS_REGION
fi

echo "======================================================"
echo " 🚀 Deploying Mini-Lambda securely to AWS EKS"
echo "======================================================"

# Ensure GITHUB_USER is set for image pulling
if [ -z "$GITHUB_USER" ]; then
    echo "⚠️  GITHUB_USER is not set in .env. Please enter your GitHub username (e.g., frustrated-dev):"
    read -r GITHUB_USER
    export GITHUB_USER
fi

echo "🔐 Proceeding with GITHUB_USER=$GITHUB_USER and RABBITMQ_URL"
echo ""

# Create the namespace first if it doesn't exist
kubectl apply -f infrastructure/k8s/manifests/namespace.yaml

echo "⚙️  Dynamically injecting variables into manifests and applying..."

# Use envsubst to replace ${GITHUB_USER} in the YAMLs without modifying the actual files on disk
# This keeps the repository clean and secure for public pushing.
envsubst < infrastructure/k8s/manifests/build-master.yaml | kubectl apply -f -
envsubst < infrastructure/k8s/manifests/build-worker.yaml | kubectl apply -f -
envsubst < infrastructure/k8s/manifests/gateway.yaml | kubectl apply -f -
envsubst < infrastructure/k8s/manifests/lambda-service.yaml | kubectl apply -f -
envsubst < infrastructure/k8s/manifests/hpa.yaml | kubectl apply -f -

# Delete any previous migration job to ensure it runs on every deploy
kubectl delete job db-migrator-job -n mini-lambda --ignore-not-found
envsubst < infrastructure/k8s/manifests/db-migrator.yaml | kubectl apply -f -

# Apply stateful components and configs
envsubst < infrastructure/k8s/manifests/configmap.yaml | kubectl apply -f -
envsubst < infrastructure/k8s/manifests/secrets.yaml | kubectl apply -f -
kubectl apply -f infrastructure/k8s/manifests/observability/

# Force restart pods to pick up the latest 'latest' tags if they haven't changed hashes
echo "🔄 Forcing rollout restart to fetch latest images..."
kubectl rollout restart deployment gateway build-master build-worker lambda-service -n mini-lambda

echo "======================================================"
echo "✅ Deployment initiated successfully!"
echo "Run 'kubectl get pods -n mini-lambda' to monitor the startup progress."
echo "======================================================"

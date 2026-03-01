#!/bin/bash
set -e

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$DIR/../../"

echo "=========================================================="
echo " 🏭 Phase 10 Load Testing: Multi-Tenant 100K RPS EKS Bench "
echo "=========================================================="

GATEWAY_URL=$(kubectl get svc gateway -n mini-lambda -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
if [ -z "$GATEWAY_URL" ]; then
    echo "⚠️  AWS ALB not provisioned yet. Fetching Gateway NodePort for testing..."
    NODE_IP=$(kubectl get nodes -o wide | awk 'NR==2{print $6}')
    NODE_PORT=$(kubectl get svc gateway -n mini-lambda -o jsonpath='{.spec.ports[0].nodePort}')
    GATEWAY_URL="http://${NODE_IP}:${NODE_PORT}"
else 
    GATEWAY_URL="http://${GATEWAY_URL}:8080"
fi
echo "🔗 Gateway URL: Mapped to $GATEWAY_URL"

echo ""
echo "📦 [Scenario 2 & 3] Building Multi-Tenant Docker Functions via GitHub..."
# Function 1: Python REST API
RES1=$(curl -s -X POST "$GATEWAY_URL/functions" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "python-hello-world",
        "runtime": "python",
        "repo_url": "https://github.com/slimslenderslacks/mcp-hello-world.git",
        "dockerfile": "Dockerfile"
    }')
FUNC1_ID=$(echo $RES1 | jq -r '.function_id')
echo "✅ Queued Python Image Build: $FUNC1_ID"

# Function 2: Node.js Data Processor
RES2=$(curl -s -X POST "$GATEWAY_URL/functions" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "node-data-processor",
        "runtime": "node",
        "repo_url": "https://github.com/LukeMwila/docker-nodejs-application.git",
        "dockerfile": "Dockerfile"
    }')
FUNC2_ID=$(echo $RES2 | jq -r '.function_id')
echo "✅ Queued Node.js Image Build: $FUNC2_ID"

# Function 3: Go Performance Math Generator
RES3=$(curl -s -X POST "$GATEWAY_URL/functions" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "go-math-processor",
        "runtime": "golang",
        "repo_url": "https://github.com/xesina/golang-echo-realworld-example-app.git",
        "dockerfile": "Dockerfile"
    }')
FUNC3_ID=$(echo $RES3 | jq -r '.function_id')
echo "✅ Queued Golang Image Build: $FUNC3_ID"

echo ""
echo "📈 [Scenario 1] Monitoring KEDA Queue Autoscaling..."
echo "Wait 30-60 seconds for build-worker pods to scale out and finish compiling ECR images..."
kubectl get hpa -n mini-lambda
kubectl get pods -n mini-lambda -l app=build-worker

echo ""
echo "⏳ Sleeping for 90 seconds to allow ECR image compilation..."
sleep 90

echo ""
echo "🚀 [Scenario 4 & 5] Launching 100K RPS k6 HTTP Traffic Blast under Rate Limit exclusions..."
if ! command -v k6 &> /dev/null
then
    echo "❌ k6 is not installed. Please install k6: brew install k6"
    exit 1
fi

export GATEWAY_URL=$GATEWAY_URL
export FUNCTION_ID_1=$FUNC1_ID
export FUNCTION_ID_2=$FUNC2_ID
export FUNCTION_ID_3=$FUNC3_ID

k6 run $DIR/k100_multi_tenant.js

echo ""
echo "✅ Multi-Tenant EKS Load Test Completed!"

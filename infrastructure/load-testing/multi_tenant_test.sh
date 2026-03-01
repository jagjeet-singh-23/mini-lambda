#!/bin/bash
set -e

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$DIR/../../"

echo "=========================================================="
echo " 🏭 Phase 10 Load Testing: Multi-Tenant 100K RPS EKS Bench "
echo "=========================================================="

echo "⏳ Waiting for AWS Application Load Balancer to provision (this takes ~2 minutes)..."
while true; do
    GATEWAY_URL=$(kubectl get ingress gateway-ingress -n mini-lambda -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
    if [ ! -z "$GATEWAY_URL" ]; then
        break
    fi
    echo -n "."
    sleep 5
done

GATEWAY_URL="http://${GATEWAY_URL}"
echo ""
echo "🔗 Gateway ALB URL: Mapped to $GATEWAY_URL"

echo ""
echo "🌍 Waiting for ALB DNS propagation and Node Target registration (this may take 2-3 minutes)..."
while true; do
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY_URL/health" || echo "000")
    if [ "$STATUS" = "200" ] || [ "$STATUS" = "404" ] || [ "$STATUS" = "405" ]; then
        echo "✅ ALB is online and responding to traffic!"
        break
    fi
    echo -n "⏳ Waiting... ($STATUS) "
    sleep 10
done

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

echo "🚀 [Scenario 4 & 5] Provisioning 5x c6a.xlarge k6 nodes via Terraform for strictly Distributed Testing..."
cd $DIR
terraform init
terraform apply -auto-approve

echo ""
echo "⏳ Waiting 60s for EC2 instances to initialize OS and install k6 via cloud-init..."
sleep 60

# Fetch IPs from Terraform output (ensure jq is installed)
WORKER_IPS=$(terraform output -json k6_worker_ips | jq -r '.[]')

echo "📤 Uploading Test Plan to all 5 EC2 worker nodes..."
for IP in $WORKER_IPS; do
  ssh-keyscan -H $IP >> ~/.ssh/known_hosts 2>/dev/null
  scp -i ~/.ssh/id_rsa $DIR/k100_multi_tenant.js ec2-user@$IP:~/
done

echo "🔥 Launching 100K RPS Distributed Test!"
echo "Targeting: $GATEWAY_URL"

for IP in $WORKER_IPS; do
  echo "--> Executing 20,000 RPS on worker $IP"
  ssh -i ~/.ssh/id_rsa ec2-user@$IP \
    "GATEWAY_URL=\"$GATEWAY_URL\" FUNCTION_ID_1=\"$FUNC1_ID\" FUNCTION_ID_2=\"$FUNC2_ID\" FUNCTION_ID_3=\"$FUNC3_ID\" TARGET_RPS=20000 k6 run ~/k100_multi_tenant.js > k6_run.stdout 2>&1 &"
done

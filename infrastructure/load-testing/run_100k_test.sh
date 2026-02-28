#!/bin/bash
set -e

# Ensure we are at relative path
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
cd "$DIR"

echo "======================================================"
echo " 🌩️ Preparing 100K RPS Distributed Load Test"
echo "======================================================"

# 1. Provide the ALB endpoint
read -p "Enter the Gateway ALB Endpoint (e.g. k8s-minilamb...elb.amazonaws.com): " ALB_ENDPOINT

echo "🚀 Provisioning 5x c6a.2xlarge JMeter nodes..."
terraform init
terraform apply -auto-approve

echo ""
echo "⏳ Waiting 60s for instances to initialize OS and install JMeter via cloud-init..."
sleep 60

# Fetch IPs from Terraform output (ensure jq is installed)
WORKER_IPS=$(terraform output -json jmeter_worker_ips | jq -r '.[]')

echo "📤 Uploading Test Plan to all 5 workers..."
for IP in $WORKER_IPS; do
  ssh-keyscan -H $IP >> ~/.ssh/known_hosts 2>/dev/null
  scp -i ~/.ssh/id_rsa ../../load_test.jmx ec2-user@$IP:~/
done

echo "🔥 Launching 100K RPS Distributed Test!"
echo "Targeting: $ALB_ENDPOINT"

# Start the test on all workers simultaneously in the background (headless mode)
for IP in $WORKER_IPS; do
  echo "--> Executing 20,000 concurrent threads on worker $IP"
  ssh -i ~/.ssh/id_rsa ec2-user@$IP \
    "~/apache-jmeter-5.6.3/bin/jmeter -n -t ~/load_test.jmx \
    -JHOST=$ALB_ENDPOINT -JPORT=80 -JTHREADS=20000 -JRAMP_UP=60 > jmeter_run.stdout 2>&1 &"
done

echo "======================================================"
echo "✅ All nodes are actively generating load!"
echo "Total Capacity: 20,000 threads x 5 nodes = 100,000 RPS"
echo "Open Grafana in your EKS cluster to monitor KEDA autoscaling and system latencies."
echo "======================================================"

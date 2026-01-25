#!/bin/bash
BASE_URL="http://localhost:8080/api/v1"  # ✅ Add /api/v1

echo "🧪 Testing Event-Driven Mini-Lambda"
echo "====================================="

# 1. Create a test function
echo ""
echo "1️⃣  Creating test function..."
FUNCTION_RESPONSE=$(curl -s -X POST "$BASE_URL/functions" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "event-test",
    "runtime": "python3.9",
    "handler": "main.handler",
    "code": "import json\nimport datetime\n\ndef handler(event, context):\n    return {\"message\": \"Event received!\", \"event\": event, \"timestamp\": str(datetime.datetime.now())}",
    "memory": 128,
    "timeout": 10
  }')

FUNCTION_ID=$(echo $FUNCTION_RESPONSE | jq -r '.function_id')  # ✅ Changed from .id
echo "✅ Function created: $FUNCTION_ID"

# 2. Create a cron trigger
echo ""
echo "2️⃣  Creating cron trigger (every minute)..."
curl -s -X POST "$BASE_URL/functions/$FUNCTION_ID/triggers/cron" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "every-minute",
    "cron_expression": "0 * * * * *",
    "timezone": "UTC",
    "enabled": true
  }' | jq .

# 3. Create a webhook
echo ""
echo "3️⃣  Creating webhook..."
curl -s -X POST "$BASE_URL/functions/$FUNCTION_ID/webhooks" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "test-webhook",
    "path": "/test-webhook",
    "secret": "my-secret-key",
    "signature_header": "X-Webhook-Signature",
    "enabled": true
  }' | jq .

# 4. Trigger webhook (no /api/v1 prefix for public endpoint)
echo ""
echo "4️⃣  Triggering webhook..."
PAYLOAD='{"test": "data"}'
SIGNATURE=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "my-secret-key" | awk '{print $2}')

curl -s -X POST "http://localhost:8080/webhooks/test-webhook" \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Signature: $SIGNATURE" \
  -d "$PAYLOAD" | jq .

# 5. List cron triggers
echo ""
echo "5️⃣  Listing cron triggers..."
curl -s "$BASE_URL/functions/$FUNCTION_ID/triggers/cron" | jq .

# 6. Invoke function
echo ""
echo "6️⃣  Invoking function directly..."
curl -s -X POST "$BASE_URL/functions/$FUNCTION_ID/invoke" \
  -H "Content-Type: application/json" \
  -d '{"direct": "invocation"}' | jq .

echo ""
echo "✅ All tests complete!"
#!/bin/bash
# test_events.sh - Test event-driven features

BASE_URL="http://localhost:8080"

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
    "code": "import json\nimport datetime\n\ndef handler(event, context):\n    return {\n        \"message\": \"Event received!\",\n        \"event\": event,\n        \"timestamp\": str(datetime.datetime.now())\n    }",
    "memory": 128,
    "timeout": "10s"
  }')

FUNCTION_ID=$(echo $FUNCTION_RESPONSE | jq -r '.id')
echo "✅ Function created: $FUNCTION_ID"

# 2. Create a cron trigger
echo ""
echo "2️⃣  Creating cron trigger (every minute)..."
curl -s -X POST "$BASE_URL/functions/$FUNCTION_ID/triggers/cron" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "every-minute",
    "expression": "0 * * * * *",
    "timezone": "UTC",
    "enabled": true
  }' | jq .
echo "✅ Cron trigger created"

# 3. Create a webhook
echo ""
echo "3️⃣  Creating webhook..."
WEBHOOK_RESPONSE=$(curl -s -X POST "$BASE_URL/functions/$FUNCTION_ID/webhooks" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "test-webhook",
    "path": "/test-webhook",
    "secret": "my-secret-key",
    "signature_header": "X-Webhook-Signature",
    "enabled": true,
    "headers": {
      "X-Custom-Header": "custom-value"
    }
  }')
echo $WEBHOOK_RESPONSE | jq .
echo "✅ Webhook created"

# 4. Trigger the webhook
echo ""
echo "4️⃣  Triggering webhook..."
PAYLOAD='{"test": "data", "timestamp": 1234567890}'
SIGNATURE=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "my-secret-key" | awk '{print $2}')

curl -s -X POST "$BASE_URL/webhooks/test-webhook" \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Signature: $SIGNATURE" \
  -d "$PAYLOAD" | jq .
echo "✅ Webhook triggered"

# 5. List cron triggers
echo ""
echo "5️⃣  Listing cron triggers..."
curl -s "$BASE_URL/functions/$FUNCTION_ID/triggers/cron" | jq .

# 6. Wait for cron to execute (60 seconds)
echo ""
echo "6️⃣  Waiting 65 seconds for cron execution..."
sleep 65

# 7. Check event audit log
echo ""
echo "7️⃣  Checking event audit log..."
curl -s "$BASE_URL/functions/$FUNCTION_ID/events?limit=10" | jq .

# 8. Check dead letter queue
echo ""
echo "8️⃣  Checking dead letter queue..."
curl -s "$BASE_URL/dlq?limit=10" | jq .

# 9. Invoke function directly (HTTP trigger)
echo ""
echo "9️⃣  Invoking function directly..."
curl -s -X POST "$BASE_URL/functions/$FUNCTION_ID/invoke" \
  -H "Content-Type: application/json" \
  -d '{"direct": "invocation"}' | jq .

echo ""
echo "✅ All tests complete!"
echo ""
echo "📊 Check RabbitMQ Management UI: http://localhost:15672"
echo "   Username: guest"
echo "   Password: guest"
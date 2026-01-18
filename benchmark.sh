#!/bin/bash

FUNCTION_ID="<function_id>"
CONCURRENT=20
SERVER_LOG="/tmp/mini-lambda-benchmark.log"

echo "==================================="
echo "Container Pool Benchmark"
echo "==================================="
echo ""

if ! curl -s http://localhost:8080/health > /dev/null 2>&1; then
    echo "❌ Error: Server is not running on localhost:8080"
    echo "Please start the server first with: ./main"
    exit 1
fi

echo "Running $CONCURRENT concurrent invocations..."
echo ""

rm -f /tmp/response_*.json

start=$(date +%s%N)

for i in $(seq 1 $CONCURRENT); do
  curl -s -X POST http://localhost:8080/functions/$FUNCTION_ID/invoke \
    -H "Content-Type: application/json" \
    -d "{\"test\": $i}" > /tmp/response_"$i".json &
done

wait

end=$(date +%s%N)
elapsed=$(((end - start) / 1000000))

echo "✅ All requests complete!"
echo ""
echo "Results:"
echo "--------"
echo "Total wall-clock time: ${elapsed}ms"
echo "Average per request: $((elapsed / CONCURRENT))ms"
echo "Requests per second: $(echo "scale=2; $CONCURRENT * 1000 / $elapsed" | bc)"
echo ""

echo "Individual execution times:"
total_duration=0
count=0
for i in $(seq 1 $CONCURRENT); do
  duration=$(jq -r '.duration_ms' /tmp/response_"$i".json 2>/dev/null)
  if [ ! -z "$duration" ] && [ "$duration" != "null" ]; then
    echo "  Request $i: ${duration}ms"
    total_duration=$((total_duration + duration))
    count=$((count + 1))
  fi
done

if [ $count -gt 0 ]; then
    avg_duration=$((total_duration / count))
    echo ""
    echo "Average execution time: ${avg_duration}ms"
fi

error_count=0
for i in $(seq 1 $CONCURRENT); do
  if grep -q "error" /tmp/response_"$i".json 2>/dev/null; then
    error_count=$((error_count + 1))
  fi
done

if [ $error_count -gt 0 ]; then
    echo "⚠️  Warning: $error_count requests had errors"
    echo "Check /tmp/response_*.json for details"
else
    echo "✅ All requests completed successfully"
fi
# Real Invoke Path Correctness Design

## Goal

Make synchronous function invocation real, testable, and measurable before doing broader scale or reliability work.

The current `/invoke` measurement is not a trustworthy execution benchmark because lambda-service returns a static stub response. This design replaces that path with a real `POST /functions/{function_id}/invoke` contract and defines tests that fail before implementation.

## API Contract

The supported synchronous invoke endpoint will be:

```http
POST /functions/{function_id}/invoke
Content-Type: application/json

{"name":"World"}
```

The request body is the raw function payload. Gateway must not parse, drain, or re-encode it.

The old `POST /invoke` endpoint is not part of the supported contract for this phase. There are no existing clients to preserve, so this can be a breaking switch. Tests and load scripts should use only the new path.

## Gateway Behavior

Gateway owns routing, rate limiting, circuit breaker wrapping, and forwarding.

For `POST /functions/{function_id}/invoke`, gateway will:

- extract `function_id` from the path;
- reject malformed paths;
- reject unsupported methods;
- apply invoke rate limiting using the path function ID;
- forward the original body bytes unchanged to lambda-service;
- forward to lambda-service as `POST /functions/{function_id}/invoke`;
- preserve request headers needed downstream;
- return downstream responses without hiding status codes.

Gateway must not decode the payload body to discover the function ID. The path is the source of truth.

## Lambda-Service Behavior

Lambda-service owns invocation semantics.

For `POST /functions/{function_id}/invoke`, lambda-service will:

1. Extract `function_id` from the path.
2. Read the request body with a configured maximum size.
3. Load the function through `FunctionService.GetFunction(ctx, id)`.
4. Execute through `runtimeManager.Execute(ctx, function, payload)`.
5. Save execution metadata through `FunctionService.SaveExecution`.
6. Return a structured JSON response.

Expected success response shape:

```json
{
  "function_id": "fn_123",
  "output": {},
  "logs": "optional/truncated logs",
  "exit_code": 0,
  "warm_start": true,
  "duration_ms": 12
}
```

The handler should be implemented behind small interfaces so tests can use fakes instead of Docker, Kubernetes, Redis, S3, or Postgres.

## Error Mapping

Lambda-service will use this mapping:

- Missing or malformed function ID: `400 Bad Request`
- Function not found: `404 Not Found`
- Payload too large: `413 Payload Too Large`
- Runtime timeout: `504 Gateway Timeout`
- Runtime execution error: `500 Internal Server Error`
- Non-zero function exit code: `200 OK` with `exit_code != 0`
- Execution-save failure: log and record metric if available, but do not fail the invoke response in this phase

Non-zero function exit is not a platform failure. The platform successfully executed the function and should return the function result metadata.

## Tests Before Implementation

Tests should be written first and should fail against the current code.

Gateway correctness tests:

- `POST /functions/fn-1/invoke` forwards to a fake lambda-service.
- The fake downstream receives the exact original body bytes.
- The fake rate limiter receives key `fn-1`.
- Wrong method returns `405 Method Not Allowed`.
- Legacy `POST /invoke` is not accepted as the success path.

Lambda-service handler tests:

- Success path loads the function, passes raw body bytes to runtime, and returns structured JSON.
- Missing function returns `404`.
- Oversized payload returns `413`.
- Runtime timeout returns `504`.
- Runtime error returns `500`.
- Non-zero function exit returns `200` with non-zero `exit_code`.
- `SaveExecution` failure does not fail the response.

Integration-style tests:

- Gateway to fake lambda-service confirms path and body preservation.
- Lambda-service handler with fake function service and fake runtime confirms real invoke flow without Docker or Kubernetes.

Benchmark validation:

- Gateway forwarding benchmark with small and medium payloads.
- Lambda handler benchmark with fake no-op runtime.
- Compare `ns/op`, `B/op`, and `allocs/op` before and after implementation.

Load-test updates:

- Update k6 scripts to call `POST /functions/{function_id}/invoke`.
- Clearly separate static gateway/service throughput from real function execution throughput.

## Implementation Boundaries

This phase is limited to synchronous invoke correctness.

Expected touched areas:

- Gateway route setup and invoke forwarding.
- Gateway tests and benchmarks for the new path.
- Lambda-service invoke handler, preferably outside `cmd/main.go`.
- Lambda-service handler tests and benchmarks.
- Load-test script path updates.
- README/API docs updates for the new invoke path.

Out of scope for this phase:

- Pool capacity race fixes.
- Pool eviction correctness.
- Runtime admission control.
- Docker/Kubernetes runner protocol redesign.
- Function artifact/code cache.
- RabbitMQ retry and DLQ redesign.
- Build idempotency reservation.
- Build queue backpressure redesign.
- Redis degraded-mode policy.
- Metrics cardinality cleanup.
- Deployment/HPA tuning.

## Deferred Work Backlog

These items were identified during the codebase review and need separate specs after the real invoke path is implemented and measurable:

1. **Pool correctness and admission control**
   - Fix concurrent pool capacity overshoot.
   - Prevent eviction of in-use containers or pods.
   - Add per-runtime admission limits and overload responses.

2. **Circuit breaker and downstream failure semantics**
   - Count selected HTTP statuses and timeouts as breaker failures.
   - Keep client-caused `4xx` responses from opening the breaker.
   - Add half-open behavior tests.

3. **RabbitMQ event retry and DLQ behavior**
   - Replace infinite requeue behavior with bounded retry count.
   - Add retry delay/backoff.
   - Ensure poison messages reach DLQ deterministically.

4. **Payload, response, and log bounds**
   - Add maximum request body size.
   - Add maximum runtime output and log size.
   - Return explicit `413` or truncation metadata.

5. **Object storage and metadata hot-path caching**
   - Avoid S3/object-storage reads on every invoke.
   - Cache function code/artifacts by immutable version or hash.
   - Cache image URI metadata for Kubernetes custom images.

6. **Runtime execution protocol**
   - Replace Docker exec polling with a long-lived runner protocol.
   - Reduce base64 payload amplification.
   - Keep Docker/Kubernetes control-plane calls off the per-invocation hot path.

7. **Rate-limit degraded mode**
   - Make Redis partition behavior explicit.
   - Avoid multiplying allowed rate by gateway replica count during Redis failures.
   - Add degraded-mode metrics.

8. **Build service correctness and throughput**
   - Make build idempotency reservation atomic before queue publish.
   - Replace per-request RabbitMQ channel creation in backpressure checks.
   - Make queue thresholds dynamic and worker-capacity aware.

9. **Observability and metrics cardinality**
   - Remove high-cardinality function IDs/names from core Prometheus labels.
   - Add runtime resource metrics: goroutines, heap, GC, DB pool stats, Redis latency, queue lag, pool saturation, cold-start rate.

10. **Deployment scaling baseline**
    - Review replica counts, resource requests/limits, HPA/KEDA behavior, and Redis/RabbitMQ/Postgres topology after real invoke benchmarks exist.

## Acceptance Criteria

This phase is complete when:

- `POST /functions/{function_id}/invoke` is the documented synchronous invoke endpoint.
- Gateway forwards the request body unchanged.
- Lambda-service executes through the real runtime manager path.
- Handler behavior is covered by failing-first tests.
- Benchmarks exist for gateway forwarding and lambda handler overhead.
- Load tests use the new path and distinguish static throughput from real execution throughput.
- Deferred reliability and scale work is tracked for later specs.

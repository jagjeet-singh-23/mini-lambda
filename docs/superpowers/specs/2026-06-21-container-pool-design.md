# Container Pool — Design Spec

**Date:** 2026-06-21  
**Status:** Approved  
**Sprint item:** #3 — Container pool (warm starts)

---

## Problem

Every function invocation currently spins up a Docker container from scratch (~200–500ms cold start). A pool of pre-warmed containers eliminates that overhead for the hot path.

---

## Decisions

| Question | Decision | Reason |
|---|---|---|
| Invocation model | `docker exec` (existing) | Fresh process per invocation = guaranteed isolation; no state leakage between callers |
| Cleanup strategy | No-op (Standard = noop) | `docker exec` creates a fresh process each time; /tmp leakage is accepted for now |
| Lifecycle goroutine | Single ticker per pool | Simplest, easy to test, fits existing `DockerPool` struct |
| MinSize replenishment | Proactive | Pool stays warm; evictions immediately trigger replacement |
| Acquire at capacity | Block until release | Prevents silent MaxSize overrun under concurrent load |

---

## Architecture

No new files. Changes are confined to the pool package, executor package, and `main.go`.

```
pool/docker_pool.go     ← Start(ctx), runLifecycle(), evictIdle(), replenish()
                           blocking Acquire via idle channel
pool/pool.go            ← Start(ctx) on ContainerPool interface
                           DefaultPoolConfig: MinSize=1, MaxSize=5, MaxIdleTime=5m
executor/docker.go      ← DockerRuntime.Start(ctx) → pool.Start(ctx)
executor/manager.go     ← Manager.Start(ctx) → all runtimes
main.go                 ← read POOL_* env vars; call runtimeManager.Start(ctx)
```

---

## Background lifecycle goroutine

`DockerPool.Start(ctx)` launches one goroutine:

```
runLifecycle(ctx):
  ticker := time.NewTicker(TickInterval)  // default 30s; injectable in tests
  loop:
    on ctx.Done → return
    on tick:
      evictIdle(ctx)   // remove StateWarm containers with LastUsed > MaxIdleTime
      replenish(ctx)   // CreateNew until len(containers) == MinSize
```

**`evictIdle`**: acquires write lock, scans warm containers, calls `removeContainerUnsafe` on stale ones.

**`replenish`**: called after evictIdle (same goroutine, no race). Checks `len < MinSize` and calls `CreateNew` sequentially for each gap. Does not hold the lock — `CreateNew` acquires it internally.

---

## Blocking Acquire

Current `Acquire` returns `nil` when no warm container is available. This is replaced with a **channel-based blocking acquire**:

- `DockerPool` holds an `idle chan *Container` (buffered, size = `MaxSize`)
- On `CreateNew`: send the new container to `idle`
- On `Release`: if container is healthy and below `MaxUseCount`, send back to `idle`; otherwise discard and call `replenish`
- On `Acquire`: `select` on `idle` and `ctx.Done()`

This caps concurrent in-flight invocations at `MaxSize` and provides natural backpressure.

---

## Env-var config

Read in `main.go` `loadConfig()`, passed to `executor.NewManager`:

| Env var | Type | Default |
|---|---|---|
| `POOL_MIN_SIZE` | int | 1 |
| `POOL_MAX_SIZE` | int | 5 |
| `POOL_IDLE_TTL` | duration | 5m |

`TickInterval` is internal (not configurable via env), defaulting to 30s. Tests inject a short value via `PoolConfig`.

---

## Start / Shutdown wiring

```
main.go:
  ctx, cancel := context.WithCancel(context.Background())   // moved before NewManager
  runtimeManager, _ := executor.NewManager(s3Storage, poolCfg)
  runtimeManager.Start(ctx)                                  // new call

executor.Manager.Start(ctx):
  for each runtime → runtime.Start(ctx)

DockerRuntime.Start(ctx):
  pool.Start(ctx)

DockerRuntime.Cleanup():
  pool.Shutdown(ctx)    // stop + remove all warm containers
  client.Close()
```

The lifecycle goroutine exits automatically when `ctx` is cancelled in `waitForShutdown`.

---

## Tests (`pool/docker_pool_test.go`)

All tests use a real Docker client. No mocks.

**Test 1 — warm reuse**
1. `CreateNew` once, `Release`
2. `Acquire` → assert same container ID, `UseCount > 0`
3. Assert `pool.Size() == 1`

**Test 2 — blocking at MaxSize**
1. Pool with `MaxSize=2`; `CreateNew` twice, both in-use
2. `Acquire` in a goroutine → blocks
3. `Release` one → blocked goroutine unblocks with that container
4. Assert `pool.Size() == 2` throughout (no new container created)

**Test 3 — TTL eviction + replenishment**
1. Pool with `MinSize=1`, `MaxIdleTime=100ms`, `TickInterval=50ms`
2. `CreateNew`, `Release`; `Start(ctx)`
3. Wait 200ms
4. Assert `pool.Size() == 1` (evicted + replaced, not drained)
5. Assert `Stats().ColdStarts == 2` (initial + replenishment)

---

## Out of scope

- `/tmp` isolation between invocations (deferred)
- Per-function pools (current per-runtime pooling is correct for the `docker exec` model)
- HTTP server inside containers (rejected: state leakage risk)

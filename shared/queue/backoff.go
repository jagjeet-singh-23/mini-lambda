// Package queue holds small, dependency-light helpers shared by the RabbitMQ
// client code in build-service and lambda-service. It intentionally does not
// try to unify the two services' connection/consumer setup — their shapes
// differ enough (single durable queue+consumer vs. topic exchange with a
// dynamic set of per-function consumers) that forcing a shared abstraction
// there would cost more than it buys. What genuinely is identical between
// them is the backoff math used when reconnecting, so that's what lives here.
package queue

import (
	"math/rand"
	"sync"
	"time"
)

// Defaults per AWS's recommended "decorrelated jitter" backoff:
// https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
const (
	DefaultBackoffBase = 500 * time.Millisecond
	DefaultBackoffCap  = 30 * time.Second
)

// Backoff computes sleep durations using the decorrelated jitter formula:
//
//	sleep = min(cap, random_between(base, prevSleep*3))
//
// On the first call (no prior sleep yet), prevSleep is treated as Base, so
// the first sleep is drawn from [base, base*3] (capped).
//
// A Backoff is safe for concurrent use.
type Backoff struct {
	Base time.Duration
	Cap  time.Duration

	mu   sync.Mutex
	rng  *rand.Rand
	prev time.Duration
}

// NewBackoff returns a Backoff configured with the recommended defaults
// (base=500ms, cap=30s).
func NewBackoff() *Backoff {
	return &Backoff{Base: DefaultBackoffBase, Cap: DefaultBackoffCap}
}

// NewBackoffWithRand returns a Backoff with an explicit base, cap, and random
// source. Passing a seeded *rand.Rand makes the sequence deterministic,
// which is useful for tests.
func NewBackoffWithRand(base, cap time.Duration, rng *rand.Rand) *Backoff {
	return &Backoff{Base: base, Cap: cap, rng: rng}
}

// Reset clears the internal "previous sleep" state. The next call to Next
// behaves as if it were the very first call again. Callers should call
// Reset after a successful (re)connection so a future, unrelated failure
// doesn't inherit an inflated backoff from a past incident.
func (b *Backoff) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prev = 0
}

// Next returns the next sleep duration per the decorrelated jitter formula.
func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	base := b.Base
	if base <= 0 {
		base = DefaultBackoffBase
	}
	maxCap := b.Cap
	if maxCap <= 0 {
		maxCap = DefaultBackoffCap
	}

	prev := b.prev
	if prev < base {
		prev = base
	}

	hi := prev * 3
	span := int64(hi - base)

	var sleep time.Duration
	if span <= 0 {
		sleep = base
	} else {
		var n int64
		if b.rng != nil {
			n = b.rng.Int63n(span)
		} else {
			n = rand.Int63n(span)
		}
		sleep = base + time.Duration(n)
	}

	if sleep > maxCap {
		sleep = maxCap
	}
	if sleep < base {
		sleep = base
	}

	b.prev = sleep
	return sleep
}

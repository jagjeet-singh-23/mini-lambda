package queue

import (
	"math/rand"
	"testing"
	"time"
)

// TestBackoff_NextStaysWithinBounds verifies that, regardless of how many
// times Next() is called (i.e. regardless of the sequence of prior sleeps),
// the returned duration never drops below Base or exceeds Cap.
func TestBackoff_NextStaysWithinBounds(t *testing.T) {
	base := 500 * time.Millisecond
	cap := 30 * time.Second

	b := NewBackoffWithRand(base, cap, rand.New(rand.NewSource(1)))

	for i := 0; i < 10_000; i++ {
		sleep := b.Next()
		if sleep < base {
			t.Fatalf("iteration %d: sleep %v below base %v", i, sleep, base)
		}
		if sleep > cap {
			t.Fatalf("iteration %d: sleep %v above cap %v", i, sleep, cap)
		}
	}
}

// TestBackoff_FirstCallShape verifies the very first call (prev sleep
// implicitly starts at Base per the decorrelated jitter formula) produces a
// value in [base, base*3], per sleep = min(cap, random(base, prev*3)) with
// prev == base.
func TestBackoff_FirstCallShape(t *testing.T) {
	base := 500 * time.Millisecond
	cap := 30 * time.Second

	for seed := int64(0); seed < 50; seed++ {
		b := NewBackoffWithRand(base, cap, rand.New(rand.NewSource(seed)))
		sleep := b.Next()
		if sleep < base || sleep > base*3 {
			t.Fatalf("seed %d: first sleep %v outside [%v, %v]", seed, sleep, base, base*3)
		}
	}
}

// TestBackoff_CapEnforced verifies that once prior sleeps grow large, the
// formula's upper bound (prev*3) is clamped to Cap rather than growing
// unbounded.
func TestBackoff_CapEnforced(t *testing.T) {
	base := 1 * time.Second
	cap := 2 * time.Second // small cap relative to base*3 growth

	b := NewBackoffWithRand(base, cap, rand.New(rand.NewSource(7)))

	for i := 0; i < 100; i++ {
		sleep := b.Next()
		if sleep > cap {
			t.Fatalf("iteration %d: sleep %v exceeds cap %v", i, sleep, cap)
		}
	}
}

// TestBackoff_Deterministic verifies that two Backoff instances seeded with
// the same rand.Source produce identical sequences — useful for callers
// that want reproducible tests of code built on top of Backoff.
func TestBackoff_Deterministic(t *testing.T) {
	base := 500 * time.Millisecond
	cap := 30 * time.Second

	b1 := NewBackoffWithRand(base, cap, rand.New(rand.NewSource(99)))
	b2 := NewBackoffWithRand(base, cap, rand.New(rand.NewSource(99)))

	for i := 0; i < 20; i++ {
		s1 := b1.Next()
		s2 := b2.Next()
		if s1 != s2 {
			t.Fatalf("iteration %d: sequences diverged: %v != %v", i, s1, s2)
		}
	}
}

// TestBackoff_ResetRestartsSequence verifies Reset() clears the internal
// "previous sleep" state so the next call behaves like the very first call
// again (bounded by [base, base*3]), instead of continuing to grow from
// wherever the sequence left off.
func TestBackoff_ResetRestartsSequence(t *testing.T) {
	base := 500 * time.Millisecond
	cap := 30 * time.Second

	b := NewBackoffWithRand(base, cap, rand.New(rand.NewSource(3)))

	// Drive prev up over several iterations.
	for i := 0; i < 10; i++ {
		b.Next()
	}

	b.Reset()
	sleep := b.Next()
	if sleep < base || sleep > base*3 {
		t.Fatalf("post-reset sleep %v outside [%v, %v]", sleep, base, base*3)
	}
}

// TestBackoff_DefaultsMatchSpec pins the AWS-recommended defaults this
// feature was built against: base=500ms, cap=30s.
func TestBackoff_DefaultsMatchSpec(t *testing.T) {
	if DefaultBackoffBase != 500*time.Millisecond {
		t.Fatalf("DefaultBackoffBase = %v, want 500ms", DefaultBackoffBase)
	}
	if DefaultBackoffCap != 30*time.Second {
		t.Fatalf("DefaultBackoffCap = %v, want 30s", DefaultBackoffCap)
	}

	b := NewBackoff()
	sleep := b.Next()
	if sleep < DefaultBackoffBase || sleep > DefaultBackoffCap {
		t.Fatalf("NewBackoff().Next() = %v, outside [%v, %v]", sleep, DefaultBackoffBase, DefaultBackoffCap)
	}
}

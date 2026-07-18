package queue

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

// panicOnCallProvider is a ConnectionProvider whose GetConnection panics if
// ever invoked. Used to prove NewBackpressureManager doesn't eagerly resolve
// a connection at construction time.
type panicOnCallProvider struct{}

func (panicOnCallProvider) GetConnection() *amqp.Connection {
	panic("connProvider.GetConnection() was called eagerly — BackpressureManager must only resolve the connection lazily, per-call")
}

// TestNewBackpressureManager_DoesNotEagerlySnapshotConnection is a
// regression test: BackpressureManager used to take a *amqp.Connection
// snapshot once at construction time (via publisher.GetConnection()),
// so once Publisher transparently reconnected after a broker drop,
// BackpressureManager kept using the old, closed connection forever —
// every GetQueueDepth call (and therefore every build-creation request)
// would fail until the process was manually restarted.
//
// Construction must not touch the provider at all; every lookup should
// go through it fresh, so BackpressureManager always sees whatever
// connection is live right now without needing to know anything about
// Publisher's reconnect logic.
func TestNewBackpressureManager_DoesNotEagerlySnapshotConnection(t *testing.T) {
	NewBackpressureManager(panicOnCallProvider{}, "test-queue")
}

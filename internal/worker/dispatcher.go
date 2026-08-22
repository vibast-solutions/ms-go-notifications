package worker

// Dispatcher is the hand-off from the accept path to the in-process send
// machinery: after the accept transaction commits, the email service calls
// Enqueue with the new message id. The real dispatcher (buffered channel +
// worker pool) arrives in VIB-9; the accept path depends only on this
// interface so it can be built and tested independently.
type Dispatcher interface {
	// Enqueue offers a queued message id for immediate processing. It is a
	// non-blocking best-effort hand-off: if the worker pool is saturated the id
	// is dropped and the sweeper will pick the row up from the database.
	Enqueue(id string)
}

// NoopDispatcher is a Dispatcher that discards every id. It is the default
// wiring until VIB-9 provides the worker pool, and is convenient in tests and
// in `serve --no-worker` mode where nothing consumes the queue in-process.
type NoopDispatcher struct{}

// Enqueue does nothing.
func (NoopDispatcher) Enqueue(string) {}

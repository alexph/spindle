package scheduler

import (
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Function definition (mirrors proto but server-side)
// ---------------------------------------------------------------------------

type FnDef struct {
	FnID        string
	Trigger     string // event name, e.g. "order/created"
	Concurrency []ConcurrencyRule
	RateLimit   []RateLimitRule
	Retries     RetryPolicy
}

type ConcurrencyRule struct {
	Limit int
	Key   string // dot-path into event data; empty = global for this fn
}

type RateLimitRule struct {
	Limit  int
	Period time.Duration
	Key    string
}

type RetryPolicy struct {
	MaxAttempts       int
	InitialBackoff    time.Duration
	BackoffMultiplier float64
}

// ---------------------------------------------------------------------------
// Event coming into the system
// ---------------------------------------------------------------------------

type Event struct {
	ID        string
	Name      string
	Data      map[string]any
	CreatedAt time.Time
}

// ---------------------------------------------------------------------------
// Work item sitting in a function's queue
// ---------------------------------------------------------------------------

type WorkItem struct {
	ID         string // stable work item id across retries and redispatch
	Event      *Event
	FnID       string
	Attempt    int
	EnqueuedAt time.Time
}

// ---------------------------------------------------------------------------
// Lease — issued to a worker for one work item
// ---------------------------------------------------------------------------

type Lease struct {
	ID        string
	WorkItem  *WorkItem
	WorkerID  string
	IssuedAt  time.Time
	ExpiresAt time.Time
	Status    LeaseStatus
	Keys      LeaseKeys
}

type LeaseStatus int

const (
	LeaseActive LeaseStatus = iota
	LeaseCompleted
	LeaseFailed
	LeaseExpired
)

type LeaseKeys struct {
	Concurrency []string
	RateLimit   []string
}

// ---------------------------------------------------------------------------
// Worker — represents one gRPC stream connection
// ---------------------------------------------------------------------------

type Worker struct {
	ID       string
	Capacity int // max concurrent
	InFlight int // current
	mu       sync.Mutex

	// Channel to send execution requests over the gRPC stream.
	// The gRPC handler goroutine reads from this and writes to the stream.
	SendCh chan *ExecutionRequest
}

type ExecutionRequest struct {
	LeaseID   string
	FnID      string
	EventID   string
	EventName string
	Data      map[string]any
	Attempt   int
}

func (w *Worker) Available() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.InFlight < w.Capacity
}

func (w *Worker) Reserve() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.InFlight >= w.Capacity {
		return false
	}
	w.InFlight++
	return true
}

func (w *Worker) Release() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.InFlight > 0 {
		w.InFlight--
	}
}

func normalizeCapacity(capacity int) int {
	if capacity <= 0 {
		return 1
	}
	return capacity
}

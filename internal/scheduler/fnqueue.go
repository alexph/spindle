package scheduler

import (
	"log"
	"sync"
	"time"
)

const (
	defaultLeaseTTL   = 30 * time.Second
	leaseExpiryPeriod = 1 * time.Second
)

// FnQueue is the dispatch brain for a single function definition.
type FnQueue struct {
	defMu sync.RWMutex
	def   FnDef

	mu    sync.Mutex
	queue []*WorkItem

	leases   map[string]*Lease
	leasesMu sync.RWMutex

	conc *ConcurrencyTracker
	rl   *RateLimiter

	workers   map[string]*Worker
	workersMu sync.RWMutex

	nudge chan struct{}

	stopOnce sync.Once
	stop     chan struct{}
}

func NewFnQueue(def FnDef) *FnQueue {
	return &FnQueue{
		def:     def,
		queue:   make([]*WorkItem, 0, 256),
		leases:  make(map[string]*Lease),
		conc:    NewConcurrencyTracker(),
		rl:      NewRateLimiter(),
		workers: make(map[string]*Worker),
		nudge:   make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
}

func (fq *FnQueue) Def() FnDef {
	fq.defMu.RLock()
	defer fq.defMu.RUnlock()
	return fq.def
}

func (fq *FnQueue) UpdateDefinition(def FnDef) {
	fq.defMu.Lock()
	fq.def = def
	fq.defMu.Unlock()
	fq.poke()
}

func (fq *FnQueue) Enqueue(item *WorkItem) bool {
	if fq.isStopped() {
		return false
	}

	item.EnqueuedAt = time.Now()

	fq.mu.Lock()
	fq.queue = append(fq.queue, item)
	fq.mu.Unlock()

	fq.poke()
	return true
}

func (fq *FnQueue) AddWorker(w *Worker) {
	fq.workersMu.Lock()
	fq.workers[w.ID] = w
	fq.workersMu.Unlock()
	fq.poke()
}

func (fq *FnQueue) RemoveWorker(workerID string) {
	fq.workersMu.Lock()
	delete(fq.workers, workerID)
	fq.workersMu.Unlock()
	fq.reclaimWorkerLeases(workerID)
}

// AckLease tolerates unknown, duplicate, expired, or late acks.
func (fq *FnQueue) AckLease(leaseID string, success bool, errMsg string) {
	fq.leasesMu.Lock()
	lease, ok := fq.leases[leaseID]
	if !ok {
		fq.leasesMu.Unlock()
		if errMsg != "" {
			log.Printf("[%s] ignoring ack for unknown lease %s: %s", fq.Def().FnID, leaseID, errMsg)
		}
		return
	}

	if success {
		lease.Status = LeaseCompleted
	} else {
		lease.Status = LeaseFailed
	}
	delete(fq.leases, leaseID)
	fq.leasesMu.Unlock()

	fq.releaseLeaseResources(lease)

	if !success {
		fq.scheduleRetry(lease.WorkItem, lease.WorkItem.Attempt+1)
	}

	fq.poke()
}

func (fq *FnQueue) Run() {
	expiryTicker := time.NewTicker(leaseExpiryPeriod)
	defer expiryTicker.Stop()

	for {
		select {
		case <-fq.stop:
			return
		case <-fq.nudge:
			fq.dispatch()
		case <-expiryTicker.C:
			fq.expireLeases()
			fq.dispatch()
		}
	}
}

func (fq *FnQueue) Stop() {
	fq.stopOnce.Do(func() {
		close(fq.stop)
	})
}

func (fq *FnQueue) Stats() FnStats {
	fq.mu.Lock()
	qLen := len(fq.queue)
	fq.mu.Unlock()

	fq.leasesMu.RLock()
	inFlight := len(fq.leases)
	fq.leasesMu.RUnlock()

	fq.workersMu.RLock()
	workerCount := len(fq.workers)
	fq.workersMu.RUnlock()

	return FnStats{
		QueueDepth: qLen,
		InFlight:   inFlight,
		Workers:    workerCount,
	}
}

func (fq *FnQueue) dispatch() {
	if fq.isStopped() {
		return
	}

	fq.mu.Lock()
	defer fq.mu.Unlock()

	def := fq.Def()
	now := time.Now()
	remaining := make([]*WorkItem, 0, len(fq.queue))

	for _, item := range fq.queue {
		worker := fq.pickWorker()
		if worker == nil {
			remaining = append(remaining, item)
			continue
		}

		concKeys := ResolveConcurrencyKeys(def.Concurrency, item.Event.Data)
		if len(def.Concurrency) > 0 && !fq.conc.Acquire(def.Concurrency, concKeys) {
			worker.Release()
			remaining = append(remaining, item)
			continue
		}

		reservation := fq.rl.Reserve(def.RateLimit, ResolveRateLimitKeys(def.RateLimit, item.Event.Data), now)
		if len(def.RateLimit) > 0 && reservation == nil {
			if len(def.Concurrency) > 0 {
				fq.conc.Release(def.Concurrency, concKeys)
			}
			worker.Release()
			remaining = append(remaining, item)
			continue
		}

		lease := &Lease{
			ID:        generateID("lease"),
			WorkItem:  item,
			WorkerID:  worker.ID,
			IssuedAt:  now,
			ExpiresAt: now.Add(defaultLeaseTTL),
			Status:    LeaseActive,
			Keys: LeaseKeys{
				Concurrency: append([]string(nil), concKeys...),
				RateLimit:   reservation.Keys(),
			},
		}

		exec := &ExecutionRequest{
			LeaseID:   lease.ID,
			FnID:      item.FnID,
			EventID:   item.Event.ID,
			EventName: item.Event.Name,
			Data:      item.Event.Data,
			Attempt:   item.Attempt,
		}

		select {
		case worker.SendCh <- exec:
			if !reservation.Commit() {
				worker.Release()
				if len(def.Concurrency) > 0 {
					fq.conc.Release(def.Concurrency, concKeys)
				}
				remaining = append(remaining, item)
				continue
			}

			fq.leasesMu.Lock()
			fq.leases[lease.ID] = lease
			fq.leasesMu.Unlock()

			log.Printf("[%s] dispatched %s to worker %s (attempt %d)",
				def.FnID, item.ID, worker.ID, item.Attempt)
		default:
			worker.Release()
			if len(def.Concurrency) > 0 {
				fq.conc.Release(def.Concurrency, concKeys)
			}
			remaining = append(remaining, item)
		}
	}

	fq.queue = remaining
}

func (fq *FnQueue) pickWorker() *Worker {
	fq.workersMu.RLock()
	defer fq.workersMu.RUnlock()

	for _, w := range fq.workers {
		if w.Reserve() {
			return w
		}
	}
	return nil
}

func (fq *FnQueue) expireLeases() {
	now := time.Now()
	var expired []*Lease

	fq.leasesMu.Lock()
	for id, lease := range fq.leases {
		if lease.Status == LeaseActive && now.After(lease.ExpiresAt) {
			lease.Status = LeaseExpired
			expired = append(expired, lease)
			delete(fq.leases, id)
		}
	}
	fq.leasesMu.Unlock()

	for _, lease := range expired {
		fq.releaseLeaseResources(lease)
		fq.scheduleRetry(lease.WorkItem, lease.WorkItem.Attempt+1)
	}
}

func (fq *FnQueue) reclaimWorkerLeases(workerID string) {
	var reclaimed []*Lease

	fq.leasesMu.Lock()
	for id, lease := range fq.leases {
		if lease.WorkerID == workerID && lease.Status == LeaseActive {
			lease.Status = LeaseExpired
			reclaimed = append(reclaimed, lease)
			delete(fq.leases, id)
		}
	}
	fq.leasesMu.Unlock()

	for _, lease := range reclaimed {
		fq.releaseConcurrency(lease)
		requeue := &WorkItem{
			ID:      lease.WorkItem.ID,
			Event:   lease.WorkItem.Event,
			FnID:    lease.WorkItem.FnID,
			Attempt: lease.WorkItem.Attempt,
		}
		fq.Enqueue(requeue)
	}
}

func (fq *FnQueue) releaseLeaseResources(lease *Lease) {
	fq.releaseConcurrency(lease)
	fq.releaseWorker(lease.WorkerID)
}

func (fq *FnQueue) releaseConcurrency(lease *Lease) {
	def := fq.Def()
	if len(def.Concurrency) == 0 {
		return
	}
	fq.conc.Release(def.Concurrency, lease.Keys.Concurrency)
}

func (fq *FnQueue) releaseWorker(workerID string) {
	fq.workersMu.RLock()
	w, ok := fq.workers[workerID]
	fq.workersMu.RUnlock()
	if ok {
		w.Release()
	}
}

func (fq *FnQueue) scheduleRetry(item *WorkItem, nextAttempt int) {
	def := fq.Def()
	if def.Retries.MaxAttempts <= 0 || nextAttempt > def.Retries.MaxAttempts {
		return
	}

	retryItem := &WorkItem{
		ID:      item.ID,
		Event:   item.Event,
		FnID:    item.FnID,
		Attempt: nextAttempt,
	}

	backoff := fq.calcBackoff(nextAttempt - 1)
	go func() {
		timer := time.NewTimer(backoff)
		defer timer.Stop()

		select {
		case <-fq.stop:
			return
		case <-timer.C:
		}

		if !fq.Enqueue(retryItem) {
			return
		}
	}()
}

func (fq *FnQueue) calcBackoff(previousAttempt int) time.Duration {
	def := fq.Def()
	base := def.Retries.InitialBackoff
	if base <= 0 {
		base = time.Second
	}
	mult := def.Retries.BackoffMultiplier
	if mult <= 0 {
		mult = 2.0
	}

	d := base
	for i := 1; i < previousAttempt; i++ {
		d = time.Duration(float64(d) * mult)
	}
	if d > 5*time.Minute {
		return 5 * time.Minute
	}
	return d
}

func (fq *FnQueue) poke() {
	if fq.isStopped() {
		return
	}

	select {
	case fq.nudge <- struct{}{}:
	default:
	}
}

func (fq *FnQueue) isStopped() bool {
	select {
	case <-fq.stop:
		return true
	default:
		return false
	}
}

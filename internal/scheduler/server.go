package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Server is the top-level scheduler coordinator.
type Server struct {
	mu sync.RWMutex

	functions map[string]*FnQueue
	triggers  map[string][]string
	workers   map[string]*WorkerRegistration
}

type WorkerRegistration struct {
	Worker *Worker
	FnIDs  map[string]struct{}
}

func NewServer() *Server {
	return &Server{
		functions: make(map[string]*FnQueue),
		triggers:  make(map[string][]string),
		workers:   make(map[string]*WorkerRegistration),
	}
}

// RegisterFunction registers or updates a function in place.
func (s *Server) RegisterFunction(def FnDef) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.functions[def.FnID]; ok {
		oldTrigger := existing.Def().Trigger
		existing.UpdateDefinition(def)
		if oldTrigger != def.Trigger {
			s.removeTriggerLocked(oldTrigger, def.FnID)
			s.triggers[def.Trigger] = appendUnique(s.triggers[def.Trigger], def.FnID)
		}
		log.Printf("updated function %s (trigger: %s)", def.FnID, def.Trigger)
		return
	}

	fq := NewFnQueue(def)
	s.functions[def.FnID] = fq
	s.triggers[def.Trigger] = appendUnique(s.triggers[def.Trigger], def.FnID)

	for _, reg := range s.workers {
		if _, ok := reg.FnIDs[def.FnID]; ok {
			fq.AddWorker(reg.Worker)
		}
	}

	go fq.Run()
	log.Printf("registered function %s (trigger: %s)", def.FnID, def.Trigger)
}

// ConnectWorker registers a worker and attaches it to its function set.
func (s *Server) ConnectWorker(workerID string, fnIDs []string, capacity int) *Worker {
	w := &Worker{
		ID:       workerID,
		Capacity: normalizeCapacity(capacity),
		SendCh:   make(chan *ExecutionRequest, normalizeCapacity(capacity)*2),
	}

	reg := &WorkerRegistration{
		Worker: w,
		FnIDs:  make(map[string]struct{}, len(fnIDs)),
	}
	for _, fnID := range fnIDs {
		reg.FnIDs[fnID] = struct{}{}
	}

	s.mu.Lock()
	if old, ok := s.workers[workerID]; ok {
		for fnID := range old.FnIDs {
			if fq, exists := s.functions[fnID]; exists {
				fq.RemoveWorker(workerID)
			}
		}
	}
	s.workers[workerID] = reg

	for fnID := range reg.FnIDs {
		if fq, ok := s.functions[fnID]; ok {
			fq.AddWorker(w)
		}
	}
	s.mu.Unlock()

	log.Printf("worker %s connected (capacity: %d, functions: %v)", workerID, w.Capacity, fnIDs)
	return w
}

// DisconnectWorker removes a worker and reclaims its leases.
func (s *Server) DisconnectWorker(workerID string) {
	s.mu.Lock()
	reg, ok := s.workers[workerID]
	if ok {
		delete(s.workers, workerID)
		for fnID := range reg.FnIDs {
			if fq, exists := s.functions[fnID]; exists {
				fq.RemoveWorker(workerID)
			}
		}
	}
	s.mu.Unlock()

	if ok {
		log.Printf("worker %s disconnected, leases reclaimed", workerID)
	}
}

// Stop shuts down all function queues.
func (s *Server) Stop() {
	s.mu.RLock()
	queues := make([]*FnQueue, 0, len(s.functions))
	for _, fq := range s.functions {
		queues = append(queues, fq)
	}
	s.mu.RUnlock()

	for _, fq := range queues {
		fq.Stop()
	}
}

// IngestEvent resolves an event to interested functions and enqueues work.
func (s *Server) IngestEvent(name string, data map[string]any) string {
	event := &Event{
		ID:        generateID("evt"),
		Name:      name,
		Data:      data,
		CreatedAt: time.Now(),
	}

	s.mu.RLock()
	fnIDs := append([]string(nil), s.triggers[name]...)
	queues := make([]*FnQueue, 0, len(fnIDs))
	for _, fnID := range fnIDs {
		if fq, ok := s.functions[fnID]; ok {
			queues = append(queues, fq)
		}
	}
	s.mu.RUnlock()

	if len(queues) == 0 {
		log.Printf("event %s (%s) has no subscribers, dropping", event.ID, name)
		return event.ID
	}

	for _, fq := range queues {
		def := fq.Def()
		item := &WorkItem{
			ID:         fmt.Sprintf("%s:%s", event.ID, def.FnID),
			Event:      event,
			FnID:       def.FnID,
			Attempt:    1,
			EnqueuedAt: time.Now(),
		}
		fq.Enqueue(item)
	}

	log.Printf("event %s (%s) fanned out to %d functions", event.ID, name, len(queues))
	return event.ID
}

// AckLease routes a lease ack to the right function queue.
func (s *Server) AckLease(fnID string, leaseID string, success bool, errMsg string) {
	s.mu.RLock()
	fq, ok := s.functions[fnID]
	s.mu.RUnlock()
	if !ok {
		log.Printf("ack for unknown function %s", fnID)
		return
	}

	fq.AckLease(leaseID, success, errMsg)
}

// Stats returns a snapshot of queue depths and in-flight counts.
func (s *Server) Stats() map[string]FnStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]FnStats, len(s.functions))
	for fnID, fq := range s.functions {
		stats[fnID] = fq.Stats()
	}
	return stats
}

type FnStats struct {
	QueueDepth int
	InFlight   int
	Workers    int
}

func (s *Server) removeTriggerLocked(trigger, fnID string) {
	if trigger == "" {
		return
	}

	ids := s.triggers[trigger]
	if len(ids) == 0 {
		return
	}

	dst := ids[:0]
	for _, id := range ids {
		if id != fnID {
			dst = append(dst, id)
		}
	}
	if len(dst) == 0 {
		delete(s.triggers, trigger)
		return
	}
	s.triggers[trigger] = dst
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

package scheduler

import (
	"strconv"
	"sync"
)

// ConcurrencyTracker enforces per-key concurrency limits.
// For a rule like {limit: 2, key: "data.customer_id"}, it tracks
// how many in-flight items share each resolved key value.
type ConcurrencyTracker struct {
	mu       sync.Mutex
	counters map[string]int // "rule_index:key_value" → count
}

func NewConcurrencyTracker() *ConcurrencyTracker {
	return &ConcurrencyTracker{
		counters: make(map[string]int),
	}
}

// Acquire tries to take a slot for each concurrency rule.
// Returns true if ALL rules pass. If any fail, nothing is acquired.
func (ct *ConcurrencyTracker) Acquire(rules []ConcurrencyRule, keyValues []string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	// Pre-check: all rules must pass
	keys := make([]string, len(rules))
	for i, rule := range rules {
		k := compositeKey(i, keyValues[i])
		keys[i] = k
		if ct.counters[k] >= rule.Limit {
			return false
		}
	}

	// All passed — increment
	for _, k := range keys {
		ct.counters[k]++
	}
	return true
}

// Release decrements counters when a lease completes/expires.
func (ct *ConcurrencyTracker) Release(rules []ConcurrencyRule, keyValues []string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	for i := range rules {
		k := compositeKey(i, keyValues[i])
		if ct.counters[k] > 0 {
			ct.counters[k]--
		}
		if ct.counters[k] == 0 {
			delete(ct.counters, k)
		}
	}
}

func compositeKey(ruleIndex int, value string) string {
	return strconv.Itoa(ruleIndex) + ":" + value
}

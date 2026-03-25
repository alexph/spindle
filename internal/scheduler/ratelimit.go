package scheduler

import (
	"sync"
	"time"
)

// RateLimiter implements a per-key token bucket.
// Reservation is split into check/commit so tokens are only charged after
// dispatch is successfully assigned to a worker.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket // "rule_index:key_value" -> bucket
}

type bucket struct {
	tokens   float64
	limit    int
	period   time.Duration
	lastFill time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*bucket),
	}
}

func (rl *RateLimiter) Reserve(rules []RateLimitRule, keyValues []string, now time.Time) *RateLimitReservation {
	if len(rules) == 0 {
		return &RateLimitReservation{limiter: rl}
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	entries := make([]reservationEntry, len(rules))
	for i, rule := range rules {
		if rule.Limit <= 0 || rule.Period <= 0 {
			return nil
		}

		key := compositeKey(i, keyValues[i])
		b := rl.bucketForRuleLocked(key, rule, now)
		if b.tokens < 1.0 {
			return nil
		}

		entries[i] = reservationEntry{key: key, bucket: b}
	}

	return &RateLimitReservation{
		limiter: rl,
		keys:    append([]string(nil), keyValues...),
		entries: entries,
	}
}

type RateLimitReservation struct {
	limiter *RateLimiter
	keys    []string
	entries []reservationEntry
	used    bool
}

type reservationEntry struct {
	key    string
	bucket *bucket
}

func (r *RateLimitReservation) Keys() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.keys...)
}

func (r *RateLimitReservation) Commit() bool {
	if r == nil {
		return false
	}
	if r.used {
		return false
	}

	r.limiter.mu.Lock()
	defer r.limiter.mu.Unlock()

	for _, entry := range r.entries {
		if entry.bucket.tokens < 1.0 {
			return false
		}
	}
	for _, entry := range r.entries {
		entry.bucket.tokens--
	}

	r.used = true
	return true
}

func (rl *RateLimiter) bucketForRuleLocked(key string, rule RateLimitRule, now time.Time) *bucket {
	b, exists := rl.buckets[key]
	if !exists || b.limit != rule.Limit || b.period != rule.Period {
		b = &bucket{
			tokens:   float64(rule.Limit),
			limit:    rule.Limit,
			period:   rule.Period,
			lastFill: now,
		}
		rl.buckets[key] = b
		return b
	}

	elapsed := now.Sub(b.lastFill)
	if elapsed > 0 {
		refill := elapsed.Seconds() / b.period.Seconds() * float64(b.limit)
		b.tokens += refill
		if b.tokens > float64(b.limit) {
			b.tokens = float64(b.limit)
		}
		b.lastFill = now
	}

	return b
}

package scheduler

import (
	"testing"
	"time"
)

func TestBasicDispatch(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "process-order",
		Trigger: "order/created",
	})

	w := s.ConnectWorker("worker-1", []string{"process-order"}, 2)

	for i := 0; i < 3; i++ {
		s.IngestEvent("order/created", map[string]any{
			"order_id":    i,
			"customer_id": "cust_1",
		})
	}

	for i := 0; i < 3; i++ {
		exec := waitForExecution(t, w.SendCh)
		if exec.FnID != "process-order" {
			t.Fatalf("unexpected function: %s", exec.FnID)
		}
		s.AckLease("process-order", exec.LeaseID, true, "")
	}
}

func TestRegisterFunctionUpdateRebindsWorkersAndTriggers(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "send-email",
		Trigger: "email/queued",
	})

	w := s.ConnectWorker("worker-1", []string{"send-email"}, 1)

	s.RegisterFunction(FnDef{
		FnID:    "send-email",
		Trigger: "email/ready",
	})

	s.IngestEvent("email/queued", map[string]any{"id": "old"})
	assertNoExecution(t, w.SendCh, 150*time.Millisecond)

	s.IngestEvent("email/ready", map[string]any{"id": "new"})
	exec := waitForExecution(t, w.SendCh)
	if exec.EventName != "email/ready" {
		t.Fatalf("expected updated trigger to dispatch, got %s", exec.EventName)
	}
	s.AckLease("send-email", exec.LeaseID, true, "")
}

func TestConcurrencyLimitByKey(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "process-payment",
		Trigger: "payment/initiated",
		Concurrency: []ConcurrencyRule{
			{Limit: 1, Key: "customer_id"},
		},
	})

	w := s.ConnectWorker("worker-1", []string{"process-payment"}, 2)

	s.IngestEvent("payment/initiated", map[string]any{"customer_id": "cust-A"})
	s.IngestEvent("payment/initiated", map[string]any{"customer_id": "cust-A"})

	first := waitForExecution(t, w.SendCh)
	assertNoExecution(t, w.SendCh, 200*time.Millisecond)

	s.AckLease("process-payment", first.LeaseID, true, "")

	second := waitForExecution(t, w.SendCh)
	s.AckLease("process-payment", second.LeaseID, true, "")
}

func TestRateLimitIsNotChargedWithoutWorker(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "rl-fn",
		Trigger: "rl/event",
		RateLimit: []RateLimitRule{
			{Limit: 1, Period: time.Second, Key: "account_id"},
		},
	})

	s.IngestEvent("rl/event", map[string]any{"account_id": "acct-1"})

	w := s.ConnectWorker("worker-1", []string{"rl-fn"}, 1)
	first := waitForExecution(t, w.SendCh)
	s.AckLease("rl-fn", first.LeaseID, true, "")

	s.IngestEvent("rl/event", map[string]any{"account_id": "acct-1"})
	assertNoExecution(t, w.SendCh, 200*time.Millisecond)
}

func TestWorkerDisconnectRedispatchesSameAttempt(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "send-email",
		Trigger: "email/queued",
	})

	w1 := s.ConnectWorker("worker-1", []string{"send-email"}, 1)
	s.IngestEvent("email/queued", map[string]any{"to": "user@test.com"})

	first := waitForExecution(t, w1.SendCh)
	if first.Attempt != 1 {
		t.Fatalf("expected first attempt to be 1, got %d", first.Attempt)
	}

	s.DisconnectWorker("worker-1")

	w2 := s.ConnectWorker("worker-2", []string{"send-email"}, 1)
	redispatched := waitForExecution(t, w2.SendCh)
	if redispatched.Attempt != 1 {
		t.Fatalf("expected redispatch to keep attempt 1, got %d", redispatched.Attempt)
	}
	s.AckLease("send-email", redispatched.LeaseID, true, "")
}

func TestRetryPreservesWorkIDAndIncrementsAttempt(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "flaky-fn",
		Trigger: "test/retry",
		Retries: RetryPolicy{
			MaxAttempts:       3,
			InitialBackoff:    20 * time.Millisecond,
			BackoffMultiplier: 1,
		},
	})

	w := s.ConnectWorker("worker-1", []string{"flaky-fn"}, 1)
	s.IngestEvent("test/retry", map[string]any{"key": "val"})

	first := waitForExecution(t, w.SendCh)
	firstLease := leaseForID(t, s, "flaky-fn", first.LeaseID)
	firstWorkID := firstLease.WorkItem.ID
	s.AckLease("flaky-fn", first.LeaseID, false, "transient")

	second := waitForExecution(t, w.SendCh)
	secondLease := leaseForID(t, s, "flaky-fn", second.LeaseID)

	if second.Attempt != 2 {
		t.Fatalf("expected retry attempt 2, got %d", second.Attempt)
	}
	if secondLease.WorkItem.ID != firstWorkID {
		t.Fatalf("expected stable work id across retry, got %s vs %s", secondLease.WorkItem.ID, firstWorkID)
	}

	s.AckLease("flaky-fn", second.LeaseID, true, "")
}

func TestAckLeaseIgnoresDuplicateAndExpiredAcks(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "ack-fn",
		Trigger: "ack/test",
		Retries: RetryPolicy{
			MaxAttempts:       2,
			InitialBackoff:    10 * time.Millisecond,
			BackoffMultiplier: 1,
		},
	})

	w := s.ConnectWorker("worker-1", []string{"ack-fn"}, 1)
	s.IngestEvent("ack/test", map[string]any{"id": 1})

	first := waitForExecution(t, w.SendCh)
	s.AckLease("ack-fn", first.LeaseID, true, "")
	s.AckLease("ack-fn", first.LeaseID, true, "")

	s.IngestEvent("ack/test", map[string]any{"id": 2})
	second := waitForExecution(t, w.SendCh)

	s.mu.RLock()
	fq := s.functions["ack-fn"]
	s.mu.RUnlock()

	fq.leasesMu.Lock()
	lease := fq.leases[second.LeaseID]
	lease.ExpiresAt = time.Now().Add(-time.Millisecond)
	fq.leasesMu.Unlock()

	retried := waitForExecution(t, w.SendCh)
	s.AckLease("ack-fn", second.LeaseID, true, "")
	if retried.Attempt != 2 {
		t.Fatalf("expected expired lease to retry as attempt 2, got %d", retried.Attempt)
	}
	s.AckLease("ack-fn", retried.LeaseID, true, "")
}

func TestResolveKeySupportsCommonGoTypes(t *testing.T) {
	data := map[string]any{
		"s":   "value",
		"i":   int(7),
		"i64": int64(8),
		"u64": uint64(9),
		"f":   3.25,
		"b":   true,
	}

	cases := map[string]string{
		"s":   "value",
		"i":   "7",
		"i64": "8",
		"u64": "9",
		"f":   "3.25",
		"b":   "true",
		"z":   globalKey,
	}

	for key, want := range cases {
		if got := ResolveKey(data, key); got != want {
			t.Fatalf("ResolveKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestStatsReflectQueuedAndInFlightWork(t *testing.T) {
	s := NewServer()
	t.Cleanup(s.Stop)

	s.RegisterFunction(FnDef{
		FnID:    "stats-fn",
		Trigger: "stats/test",
	})

	for i := 0; i < 2; i++ {
		s.IngestEvent("stats/test", map[string]any{"i": i})
	}

	waitForCondition(t, time.Second, func() bool {
		stats := s.Stats()["stats-fn"]
		return stats.QueueDepth == 2 && stats.Workers == 0
	})

	w := s.ConnectWorker("worker-1", []string{"stats-fn"}, 1)
	exec := waitForExecution(t, w.SendCh)

	waitForCondition(t, time.Second, func() bool {
		stats := s.Stats()["stats-fn"]
		return stats.QueueDepth == 1 && stats.InFlight == 1 && stats.Workers == 1
	})

	s.AckLease("stats-fn", exec.LeaseID, true, "")
}

func waitForExecution(t *testing.T, ch <-chan *ExecutionRequest) *ExecutionRequest {
	t.Helper()

	select {
	case exec := <-ch:
		return exec
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for execution")
		return nil
	}
}

func assertNoExecution(t *testing.T, ch <-chan *ExecutionRequest, d time.Duration) {
	t.Helper()

	select {
	case exec := <-ch:
		t.Fatalf("unexpected execution: %+v", exec)
	case <-time.After(d):
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("condition not met before timeout")
}

func leaseForID(t *testing.T, s *Server, fnID, leaseID string) *Lease {
	t.Helper()

	s.mu.RLock()
	fq := s.functions[fnID]
	s.mu.RUnlock()
	if fq == nil {
		t.Fatalf("missing function queue %s", fnID)
	}

	fq.leasesMu.RLock()
	lease := fq.leases[leaseID]
	fq.leasesMu.RUnlock()
	if lease == nil {
		t.Fatalf("missing lease %s", leaseID)
	}
	return lease
}

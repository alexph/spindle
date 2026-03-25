package grpc

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	spindlepb "github.com/alexph/spindle/internal/gen/spindlepb"
	"github.com/alexph/spindle/internal/scheduler"
	gg "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestConvertFnDefParsesDurations(t *testing.T) {
	def, err := convertFnDef(&spindlepb.FnDef{
		FnId: "fn-1",
		Trigger: &spindlepb.Trigger{
			Event: "orders/created",
		},
		Concurrency: []*spindlepb.Concurrency{
			{Limit: 2, Key: "customer_id"},
		},
		RateLimit: []*spindlepb.RateLimit{
			{Limit: 5, Period: "2s", Key: "customer_id"},
		},
		Retries: &spindlepb.RetryPolicy{
			MaxAttempts:       3,
			InitialBackoff:    "150ms",
			BackoffMultiplier: 1.5,
		},
	})
	if err != nil {
		t.Fatalf("convertFnDef returned error: %v", err)
	}

	if def.Trigger != "orders/created" {
		t.Fatalf("unexpected trigger: %s", def.Trigger)
	}
	if len(def.RateLimit) != 1 || def.RateLimit[0].Period != 2*time.Second {
		t.Fatalf("unexpected rate limit period: %+v", def.RateLimit)
	}
	if def.Retries.InitialBackoff != 150*time.Millisecond {
		t.Fatalf("unexpected retry backoff: %s", def.Retries.InitialBackoff)
	}
}

func TestConvertFnDefRejectsInvalidDuration(t *testing.T) {
	_, err := convertFnDef(&spindlepb.FnDef{
		FnId: "fn-1",
		Trigger: &spindlepb.Trigger{
			Event: "orders/created",
		},
		RateLimit: []*spindlepb.RateLimit{
			{Limit: 1, Period: "not-a-duration"},
		},
	})
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
}

func TestSendEventRejectsInvalidJSON(t *testing.T) {
	svc := NewService(scheduler.NewServer())

	_, err := svc.SendEvent(context.Background(), &spindlepb.SendEventRequest{
		EventName: "orders/created",
		Data:      []byte(`{"broken"`),
	})
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}

	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestConnectStreamLifecycle(t *testing.T) {
	core := scheduler.NewServer()
	t.Cleanup(core.Stop)

	svc := NewService(core)
	_, err := svc.Register(context.Background(), &spindlepb.RegisterRequest{
		WorkerId: "worker-1",
		Functions: []*spindlepb.FnDef{
			{
				FnId: "send-email",
				Trigger: &spindlepb.Trigger{
					Event: "email/queued",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}

	stream := newFakeStream()
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Connect(stream)
	}()

	stream.push(&spindlepb.WorkerMessage{
		Msg: &spindlepb.WorkerMessage_Ready{
			Ready: &spindlepb.WorkerReady{
				WorkerId: "worker-1",
				Capacity: 1,
			},
		},
	})

	payload, _ := json.Marshal(map[string]any{"to": "user@test.com"})
	if _, err := svc.SendEvent(context.Background(), &spindlepb.SendEventRequest{
		EventName: "email/queued",
		Data:      payload,
	}); err != nil {
		t.Fatalf("SendEvent returned error: %v", err)
	}

	serverMsg := stream.waitForSend(t)
	exec := serverMsg.GetExec()
	if exec == nil {
		t.Fatal("expected execution request")
	}
	if exec.GetFnId() != "send-email" {
		t.Fatalf("unexpected fn_id: %s", exec.GetFnId())
	}

	stream.push(&spindlepb.WorkerMessage{
		Msg: &spindlepb.WorkerMessage_Ack{
			Ack: &spindlepb.ExecutionAck{
				LeaseId: exec.GetLeaseId(),
				Success: true,
			},
		},
	})

	waitForCondition(t, time.Second, func() bool {
		stats := core.Stats()["send-email"]
		return stats.InFlight == 0
	})

	stream.closeRecv()
	if err := <-errCh; err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
}

func TestServiceIntegrationRoundTrip(t *testing.T) {
	core := scheduler.NewServer()
	t.Cleanup(core.Stop)

	svc := NewService(core)
	_, err := svc.Register(context.Background(), &spindlepb.RegisterRequest{
		WorkerId: "worker-2",
		Functions: []*spindlepb.FnDef{
			{
				FnId: "process-order",
				Trigger: &spindlepb.Trigger{
					Event: "order/created",
				},
				Retries: &spindlepb.RetryPolicy{
					MaxAttempts:       2,
					InitialBackoff:    "10ms",
					BackoffMultiplier: 1,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}

	stream := newFakeStream()
	done := make(chan error, 1)
	go func() {
		done <- svc.Connect(stream)
	}()

	stream.push(&spindlepb.WorkerMessage{
		Msg: &spindlepb.WorkerMessage_Ready{
			Ready: &spindlepb.WorkerReady{
				WorkerId: "worker-2",
				Capacity: 1,
			},
		},
	})

	body, _ := json.Marshal(map[string]any{"order_id": 42})
	resp, err := svc.SendEvent(context.Background(), &spindlepb.SendEventRequest{
		EventName: "order/created",
		Data:      body,
	})
	if err != nil {
		t.Fatalf("SendEvent returned error: %v", err)
	}
	if resp.GetEventId() == "" {
		t.Fatal("expected event id")
	}

	first := stream.waitForSend(t)
	if first.GetExec() == nil {
		t.Fatal("expected first execution")
	}

	stream.push(&spindlepb.WorkerMessage{
		Msg: &spindlepb.WorkerMessage_Ack{
			Ack: &spindlepb.ExecutionAck{
				LeaseId: first.GetExec().GetLeaseId(),
				Success: false,
				Error:   "transient",
			},
		},
	})

	second := stream.waitForSend(t)
	if second.GetExec().GetAttempt() != 2 {
		t.Fatalf("expected retry attempt 2, got %d", second.GetExec().GetAttempt())
	}

	stream.push(&spindlepb.WorkerMessage{
		Msg: &spindlepb.WorkerMessage_Ack{
			Ack: &spindlepb.ExecutionAck{
				LeaseId: second.GetExec().GetLeaseId(),
				Success: true,
			},
		},
	})

	waitForCondition(t, time.Second, func() bool {
		stats := core.Stats()["process-order"]
		return stats.InFlight == 0 && stats.QueueDepth == 0
	})

	stream.closeRecv()
	if err := <-done; err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
}

type fakeStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	recvCh chan *spindlepb.WorkerMessage
	sendCh chan *spindlepb.ServerMessage
}

func newFakeStream() *fakeStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &fakeStream{
		ctx:    ctx,
		cancel: cancel,
		recvCh: make(chan *spindlepb.WorkerMessage, 16),
		sendCh: make(chan *spindlepb.ServerMessage, 16),
	}
}

func (s *fakeStream) push(msg *spindlepb.WorkerMessage) {
	s.recvCh <- msg
}

func (s *fakeStream) closeRecv() {
	close(s.recvCh)
	s.cancel()
}

func (s *fakeStream) waitForSend(t *testing.T) *spindlepb.ServerMessage {
	t.Helper()

	select {
	case msg := <-s.sendCh:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server send")
		return nil
	}
}

func (s *fakeStream) Context() context.Context     { return s.ctx }
func (s *fakeStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeStream) SetTrailer(metadata.MD)       {}
func (s *fakeStream) SendMsg(any) error            { return nil }
func (s *fakeStream) RecvMsg(any) error            { return nil }

func (s *fakeStream) Send(msg *spindlepb.ServerMessage) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.sendCh <- msg:
		return nil
	}
}

func (s *fakeStream) Recv() (*spindlepb.WorkerMessage, error) {
	select {
	case <-s.ctx.Done():
		return nil, io.EOF
	case msg, ok := <-s.recvCh:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	}
}

var _ gg.BidiStreamingServer[spindlepb.WorkerMessage, spindlepb.ServerMessage] = (*fakeStream)(nil)

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

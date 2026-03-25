package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	spindlepb "github.com/alexph/spindle/internal/gen/spindlepb"
	"github.com/alexph/spindle/internal/scheduler"
	gg "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service struct {
	spindlepb.UnimplementedSpindleServer

	scheduler *scheduler.Server

	mu              sync.RWMutex
	workerFunctions map[string][]string
	leaseFunctions  map[string]string
}

func NewService(s *scheduler.Server) *Service {
	return &Service{
		scheduler:       s,
		workerFunctions: make(map[string][]string),
		leaseFunctions:  make(map[string]string),
	}
}

func (s *Service) SendEvent(ctx context.Context, req *spindlepb.SendEventRequest) (*spindlepb.SendEventResponse, error) {
	_ = ctx

	if req.GetEventName() == "" {
		return nil, status.Error(codes.InvalidArgument, "event_name is required")
	}

	data, err := decodeJSONMap(req.GetData())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid event data: %v", err)
	}

	eventID := s.scheduler.IngestEvent(req.GetEventName(), data)
	return &spindlepb.SendEventResponse{EventId: eventID}, nil
}

func (s *Service) Register(ctx context.Context, req *spindlepb.RegisterRequest) (*spindlepb.RegisterResponse, error) {
	_ = ctx

	if req.GetWorkerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if len(req.GetFunctions()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one function is required")
	}

	fnIDs := make([]string, 0, len(req.GetFunctions()))
	for _, pbDef := range req.GetFunctions() {
		def, err := convertFnDef(pbDef)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid function %q: %v", pbDef.GetFnId(), err)
		}
		s.scheduler.RegisterFunction(def)
		fnIDs = append(fnIDs, def.FnID)
	}

	s.mu.Lock()
	s.workerFunctions[req.GetWorkerId()] = fnIDs
	s.mu.Unlock()

	return &spindlepb.RegisterResponse{Ok: true}, nil
}

func (s *Service) Connect(stream gg.BidiStreamingServer[spindlepb.WorkerMessage, spindlepb.ServerMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}

	ready, ok := first.GetMsg().(*spindlepb.WorkerMessage_Ready)
	if !ok || ready.Ready == nil {
		return status.Error(codes.InvalidArgument, "first stream message must be ready")
	}

	workerID := ready.Ready.GetWorkerId()
	if workerID == "" {
		return status.Error(codes.InvalidArgument, "worker_id is required")
	}

	s.mu.RLock()
	fnIDs := append([]string(nil), s.workerFunctions[workerID]...)
	s.mu.RUnlock()
	if len(fnIDs) == 0 {
		return status.Errorf(codes.FailedPrecondition, "worker %s has no registered functions", workerID)
	}

	worker := s.scheduler.ConnectWorker(workerID, fnIDs, int(ready.Ready.GetCapacity()))
	defer s.scheduler.DisconnectWorker(workerID)

	sendErrCh := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stream.Context().Done():
				sendErrCh <- nil
				return
			case exec := <-worker.SendCh:
				msg, err := executionRequestMessage(exec)
				if err != nil {
					sendErrCh <- status.Errorf(codes.Internal, "encode execution: %v", err)
					return
				}
				s.rememberLease(exec.LeaseID, exec.FnID)
				if err := stream.Send(msg); err != nil {
					s.deleteLease(exec.LeaseID)
					sendErrCh <- err
					return
				}
			}
		}
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				select {
				case sendErr := <-sendErrCh:
					return sendErr
				default:
					return nil
				}
			}
			return err
		}

		switch payload := msg.GetMsg().(type) {
		case *spindlepb.WorkerMessage_Ack:
			if payload.Ack == nil {
				return status.Error(codes.InvalidArgument, "ack message is required")
			}
			fnID, ok := s.fnIDForLease(payload.Ack.GetLeaseId())
			if !ok {
				return status.Errorf(codes.FailedPrecondition, "unknown lease %s", payload.Ack.GetLeaseId())
			}
			s.scheduler.AckLease(fnID, payload.Ack.GetLeaseId(), payload.Ack.GetSuccess(), payload.Ack.GetError())
			s.deleteLease(payload.Ack.GetLeaseId())
		default:
			return status.Error(codes.InvalidArgument, "unsupported worker message")
		}
	}
}

func convertFnDef(pb *spindlepb.FnDef) (scheduler.FnDef, error) {
	if pb == nil {
		return scheduler.FnDef{}, errors.New("function definition is required")
	}
	if pb.GetFnId() == "" {
		return scheduler.FnDef{}, errors.New("fn_id is required")
	}
	if pb.GetTrigger() == nil || pb.GetTrigger().GetEvent() == "" {
		return scheduler.FnDef{}, errors.New("trigger.event is required")
	}

	def := scheduler.FnDef{
		FnID:    pb.GetFnId(),
		Trigger: pb.GetTrigger().GetEvent(),
	}

	if len(pb.GetConcurrency()) > 0 {
		def.Concurrency = make([]scheduler.ConcurrencyRule, 0, len(pb.GetConcurrency()))
		for _, rule := range pb.GetConcurrency() {
			if rule.GetLimit() <= 0 {
				return scheduler.FnDef{}, fmt.Errorf("concurrency limit must be > 0")
			}
			def.Concurrency = append(def.Concurrency, scheduler.ConcurrencyRule{
				Limit: int(rule.GetLimit()),
				Key:   rule.GetKey(),
			})
		}
	}

	if len(pb.GetRateLimit()) > 0 {
		def.RateLimit = make([]scheduler.RateLimitRule, 0, len(pb.GetRateLimit()))
		for _, rule := range pb.GetRateLimit() {
			if rule.GetLimit() <= 0 {
				return scheduler.FnDef{}, fmt.Errorf("rate_limit limit must be > 0")
			}
			period, err := time.ParseDuration(rule.GetPeriod())
			if err != nil {
				return scheduler.FnDef{}, fmt.Errorf("rate_limit period: %w", err)
			}
			def.RateLimit = append(def.RateLimit, scheduler.RateLimitRule{
				Limit:  int(rule.GetLimit()),
				Period: period,
				Key:    rule.GetKey(),
			})
		}
	}

	if pb.GetRetries() != nil {
		var backoff time.Duration
		var err error
		if pb.GetRetries().GetInitialBackoff() != "" {
			backoff, err = time.ParseDuration(pb.GetRetries().GetInitialBackoff())
			if err != nil {
				return scheduler.FnDef{}, fmt.Errorf("retries.initial_backoff: %w", err)
			}
		}
		def.Retries = scheduler.RetryPolicy{
			MaxAttempts:       int(pb.GetRetries().GetMaxAttempts()),
			InitialBackoff:    backoff,
			BackoffMultiplier: pb.GetRetries().GetBackoffMultiplier(),
		}
	}

	return def, nil
}

func decodeJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	if data == nil {
		return map[string]any{}, nil
	}
	return data, nil
}

func executionRequestMessage(exec *scheduler.ExecutionRequest) (*spindlepb.ServerMessage, error) {
	if exec == nil {
		return nil, errors.New("execution request is nil")
	}

	data, err := json.Marshal(exec.Data)
	if err != nil {
		return nil, err
	}

	return &spindlepb.ServerMessage{
		Msg: &spindlepb.ServerMessage_Exec{
			Exec: &spindlepb.ExecutionRequest{
				LeaseId:   exec.LeaseID,
				FnId:      exec.FnID,
				EventId:   exec.EventID,
				EventName: exec.EventName,
				Data:      data,
				Attempt:   int32(exec.Attempt),
			},
		},
	}, nil
}

func (s *Service) rememberLease(leaseID, fnID string) {
	s.mu.Lock()
	s.leaseFunctions[leaseID] = fnID
	s.mu.Unlock()
}

func (s *Service) fnIDForLease(leaseID string) (string, bool) {
	s.mu.RLock()
	fnID, ok := s.leaseFunctions[leaseID]
	s.mu.RUnlock()
	return fnID, ok
}

func (s *Service) deleteLease(leaseID string) {
	s.mu.Lock()
	delete(s.leaseFunctions, leaseID)
	s.mu.Unlock()
}

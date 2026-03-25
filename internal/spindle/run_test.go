package spindle

import (
	"context"
	"testing"
	"time"
)

func TestRunWithConfigReturnsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := &Config{DBPath: t.TempDir() + "/spindle.db"}

	done := make(chan error, 1)
	go func() {
		done <- RunWithConfig(ctx, cfg)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithConfig returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunWithConfig to exit")
	}
}

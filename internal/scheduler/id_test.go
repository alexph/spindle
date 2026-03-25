package scheduler

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateIDUsesUUIDv7(t *testing.T) {
	id := generateID("evt")

	parts := strings.SplitN(id, "_", 2)
	if len(parts) != 2 {
		t.Fatalf("expected prefixed id, got %q", id)
	}
	if parts[0] != "evt" {
		t.Fatalf("unexpected prefix %q", parts[0])
	}

	parsed, err := uuid.Parse(parts[1])
	if err != nil {
		t.Fatalf("parse uuid: %v", err)
	}
	if parsed.Version() != 7 {
		t.Fatalf("expected uuid version 7, got %d", parsed.Version())
	}
}

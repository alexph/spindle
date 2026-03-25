package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunCommandInvokesRuntime(t *testing.T) {
	orig := runServer
	defer func() { runServer = orig }()

	called := false
	runServer = func(ctx context.Context) error {
		called = true
		return context.Canceled
	}

	cmd := newRootCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !called {
		t.Fatal("expected runServer to be called")
	}
	if !strings.Contains(stderr.String(), "Spindle server starting...") {
		t.Fatalf("expected startup message, got %q", stderr.String())
	}
}

func TestRunCommandReturnsRuntimeError(t *testing.T) {
	orig := runServer
	defer func() { runServer = orig }()

	runServer = func(ctx context.Context) error {
		return errors.New("boom")
	}

	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected command error")
	}
	if err.Error() != "boom" {
		t.Fatalf("unexpected error: %v", err)
	}
}

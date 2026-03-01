package sandbox_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/tools/sandbox"
	"github.com/jrimmer/chandra/pkg"
)

// shortTempDir creates a temporary directory under /tmp (not the default test
// temp dir, which can exceed the 104-byte macOS Unix socket path limit).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sbx")
	if err != nil {
		t.Fatalf("shortTempDir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// startEchoServer creates a Unix socket listener that handles a single
// SandboxRequest by echoing back a SandboxResponse with the same CallID
// and a fixed content string. It returns the socket path and a done channel
// that is closed once the server goroutine exits.
func startEchoServer(t *testing.T, socketPath string, content string) <-chan struct{} {
	t.Helper()

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("startEchoServer: listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ln.Close()

		conn, err := ln.Accept()
		if err != nil {
			// Listener was closed; this is expected during cleanup.
			return
		}
		defer conn.Close()

		var req pkg.SandboxRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			t.Errorf("startEchoServer: decode request: %v", err)
			return
		}

		resp := pkg.SandboxResponse{
			CallID:  req.CallID,
			Content: content,
		}
		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			t.Errorf("startEchoServer: encode response: %v", err)
		}
	}()

	return done
}

func TestRunner_Execute_Success(t *testing.T) {
	socketPath := filepath.Join(shortTempDir(t), "s.sock")

	const wantContent = "echo-response"
	done := startEchoServer(t, socketPath, wantContent)

	runner := sandbox.NewRunner(socketPath, 2*time.Second)

	call := pkg.ToolCall{
		ID:         "call-abc",
		Name:       "test.tool",
		Parameters: json.RawMessage(`{"key":"value"}`),
	}

	result, err := runner.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.ID != call.ID {
		t.Errorf("result.ID: want %q, got %q", call.ID, result.ID)
	}
	if result.Content != wantContent {
		t.Errorf("result.Content: want %q, got %q", wantContent, result.Content)
	}
	if result.Error != nil {
		t.Errorf("result.Error: expected nil, got %+v", result.Error)
	}

	// Wait for the server goroutine to finish cleanly.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("server goroutine did not exit in time")
	}
}

func TestRunner_Execute_ServerError(t *testing.T) {
	socketPath := filepath.Join(shortTempDir(t), "e.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ln.Close()

		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var req pkg.SandboxRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}

		// Return a response with an error field.
		resp := pkg.SandboxResponse{
			CallID: req.CallID,
			Error:  "tool exploded",
		}
		json.NewEncoder(conn).Encode(resp) //nolint:errcheck
	}()

	runner := sandbox.NewRunner(socketPath, 2*time.Second)
	call := pkg.ToolCall{ID: "err-call", Name: "broken.tool", Parameters: json.RawMessage(`{}`)}

	result, err := runner.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute returned unexpected transport error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected result.Error to be set, got nil")
	}
	if result.Error.Message != "tool exploded" {
		t.Errorf("result.Error.Message: want %q, got %q", "tool exploded", result.Error.Message)
	}
	if result.Error.Kind != pkg.ErrInternal {
		t.Errorf("result.Error.Kind: want ErrInternal, got %v", result.Error.Kind)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("server goroutine did not exit in time")
	}
}

func TestRunner_Execute_DialFailure(t *testing.T) {
	// Point at a socket path that has no listener.
	socketPath := filepath.Join(shortTempDir(t), "n.sock")
	// Ensure the file does not exist.
	os.Remove(socketPath)

	runner := sandbox.NewRunner(socketPath, 100*time.Millisecond)
	call := pkg.ToolCall{ID: "x", Name: "noop", Parameters: json.RawMessage(`{}`)}

	_, err := runner.Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected dial error, got nil")
	}
}

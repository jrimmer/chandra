package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempSocketPath returns a unique socket path short enough for macOS's
// 104-character Unix socket path limit.
func tempSocketPath(t *testing.T) string {
	t.Helper()
	// Use os.MkdirTemp with a short prefix under /tmp so the full path stays
	// well within the 104-byte sockaddr_un.sun_path limit on macOS/BSD.
	dir, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatalf("tempSocketPath: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "t.sock")
}

// TestProtocol_RoundTrip verifies that a server and client can exchange a
// request/response pair over a Unix socket.
func TestProtocol_RoundTrip(t *testing.T) {
	socketPath := tempSocketPath(t)

	srv := NewServer()
	srv.Handle("ping", func(_ context.Context, params json.RawMessage) (any, error) {
		return map[string]string{"pong": "ok"}, nil
	})

	require.NoError(t, srv.Start(socketPath))
	t.Cleanup(srv.Stop)

	client := NewClient(socketPath)
	var result map[string]string
	err := client.Call(context.Background(), "ping", nil, &result)
	require.NoError(t, err)
	assert.Equal(t, "ok", result["pong"])
}

// TestProtocol_RoundTrip_WithParams verifies that params are forwarded to the
// handler and the handler's response is correctly decoded by the client.
func TestProtocol_RoundTrip_WithParams(t *testing.T) {
	socketPath := tempSocketPath(t)

	srv := NewServer()
	srv.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var p map[string]string
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return p, nil
	})

	require.NoError(t, srv.Start(socketPath))
	t.Cleanup(srv.Stop)

	client := NewClient(socketPath)
	var result map[string]string
	err := client.Call(context.Background(), "echo", map[string]string{"hello": "world"}, &result)
	require.NoError(t, err)
	assert.Equal(t, "world", result["hello"])
}

// TestProtocol_UnknownMethod verifies that the server returns an error for an
// unregistered method and the client surfaces it.
func TestProtocol_UnknownMethod(t *testing.T) {
	socketPath := tempSocketPath(t)

	srv := NewServer()
	require.NoError(t, srv.Start(socketPath))
	t.Cleanup(srv.Stop)

	client := NewClient(socketPath)
	err := client.Call(context.Background(), "does.not.exist", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown method")
}

// TestProtocol_HandlerError verifies that a handler error is propagated to the
// client as an error return value.
func TestProtocol_HandlerError(t *testing.T) {
	socketPath := tempSocketPath(t)

	srv := NewServer()
	srv.Handle("fail", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, errBoom
	})

	require.NoError(t, srv.Start(socketPath))
	t.Cleanup(srv.Stop)

	client := NewClient(socketPath)
	err := client.Call(context.Background(), "fail", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

// errBoom is a sentinel error used in tests.
var errBoom = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }

// TestProtocol_StaleSocketCleanup verifies that Start removes a socket file
// that has no process listening behind it, and successfully binds in its place.
func TestProtocol_StaleSocketCleanup(t *testing.T) {
	socketPath := tempSocketPath(t)

	// Create a stale socket file (just a regular file — no process behind it).
	f, err := os.Create(socketPath)
	require.NoError(t, err)
	f.Close()

	srv := NewServer()
	srv.Handle("ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"pong": "ok"}, nil
	})

	// Start must succeed despite the stale file.
	require.NoError(t, srv.Start(socketPath))
	t.Cleanup(srv.Stop)

	// Actual requests must be served correctly after cleanup.
	client := NewClient(socketPath)
	var result map[string]string
	err = client.Call(context.Background(), "ping", nil, &result)
	require.NoError(t, err)
	assert.Equal(t, "ok", result["pong"])
}

// TestProtocol_AlreadyRunning verifies that Start returns an error when an
// active daemon is already bound to the socket path.
func TestProtocol_AlreadyRunning(t *testing.T) {
	socketPath := tempSocketPath(t)

	first := NewServer()
	require.NoError(t, first.Start(socketPath))
	t.Cleanup(first.Stop)

	second := NewServer()
	err := second.Start(socketPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
}

// TestSocketPath verifies that SocketPath returns a non-empty path using the
// XDG_RUNTIME_DIR environment variable when set.
func TestSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	path := SocketPath()
	assert.Equal(t, "/run/user/1000/chandra/chandra.sock", path)
}

// TestSocketPath_Fallback verifies that SocketPath falls back to the temp-dir
// variant when XDG_RUNTIME_DIR is unset.
func TestSocketPath_Fallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	path := SocketPath()
	assert.NotEmpty(t, path)
	assert.Contains(t, path, "chandra-")
	assert.Contains(t, path, "chandra.sock")
}

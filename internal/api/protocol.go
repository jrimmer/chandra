// Package api provides the Unix socket JSON protocol used for communication
// between the chandra CLI and the chandrad daemon.
package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Request is sent from the CLI to the daemon over the Unix socket.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is sent from the daemon to the CLI over the Unix socket.
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// SocketPath returns the canonical Unix socket path for the daemon.
// It prefers $XDG_RUNTIME_DIR/chandra/chandra.sock and falls back to
// /tmp/chandra-<uid>/chandra.sock.
func SocketPath() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "chandra", "chandra.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("chandra-%d", os.Getuid()), "chandra.sock")
}

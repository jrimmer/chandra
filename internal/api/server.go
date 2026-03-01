package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// HandlerFunc is the function signature for method handlers registered with
// the Server. It receives the raw JSON params and returns a result or error.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Server listens on a Unix socket and dispatches incoming JSON requests to
// registered handlers.
type Server struct {
	handlers map[string]HandlerFunc
	listener net.Listener
	mu       sync.Mutex
	wg       sync.WaitGroup
	quit     chan struct{}
}

// NewServer creates a new Server with no handlers registered.
func NewServer() *Server {
	return &Server{
		handlers: make(map[string]HandlerFunc),
		quit:     make(chan struct{}),
	}
}

// Handle registers a HandlerFunc for the given method name.
// Calling Handle after Start has undefined behavior.
func (s *Server) Handle(method string, h HandlerFunc) {
	s.handlers[method] = h
}

// Start binds the Unix socket at socketPath and begins accepting connections.
// If a stale socket file exists with no process behind it, it is removed and
// the bind proceeds. If an active daemon is already listening, Start returns
// an error.
func (s *Server) Start(socketPath string) error {
	// 1. Create socket directory with mode 0700.
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("api: create socket dir: %w", err)
	}

	// 2. Handle existing socket file.
	if _, err := os.Stat(socketPath); err == nil {
		// File exists — try to connect to it.
		conn, dialErr := net.Dial("unix", socketPath)
		if dialErr == nil {
			// A daemon is already listening.
			conn.Close()
			return fmt.Errorf("api: daemon already running at %s", socketPath)
		}
		// No process listening — remove the stale socket.
		if removeErr := os.Remove(socketPath); removeErr != nil {
			return fmt.Errorf("api: remove stale socket: %w", removeErr)
		}
	}

	// 3. Bind the socket.
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("api: listen: %w", err)
	}

	// 4. Restrict socket permissions to owner-only.
	if err := os.Chmod(socketPath, 0600); err != nil {
		ln.Close()
		return fmt.Errorf("api: chmod socket: %w", err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	// 5. Accept loop in background goroutine.
	s.wg.Add(1)
	go s.acceptLoop(ln)

	return nil
}

// Stop closes the listener and waits for all in-flight connections to finish.
func (s *Server) Stop() {
	close(s.quit)

	s.mu.Lock()
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
}

// acceptLoop accepts connections until the listener is closed.
func (s *Server) acceptLoop(ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener closed — normal shutdown.
			select {
			case <-s.quit:
				return
			default:
				// Unexpected error; stop accepting.
				return
			}
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// handleConn reads one Request from conn, dispatches it, and writes the Response.
func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req Request
	if err := dec.Decode(&req); err != nil {
		// Malformed request — send error response.
		_ = enc.Encode(Response{Error: fmt.Sprintf("api: decode request: %v", err)})
		return
	}

	handler, ok := s.handlers[req.Method]
	if !ok {
		_ = enc.Encode(Response{Error: fmt.Sprintf("api: unknown method: %s", req.Method)})
		return
	}

	result, err := handler(context.Background(), req.Params)
	if err != nil {
		_ = enc.Encode(Response{Error: err.Error()})
		return
	}

	raw, err := json.Marshal(result)
	if err != nil {
		_ = enc.Encode(Response{Error: fmt.Sprintf("api: marshal result: %v", err)})
		return
	}

	_ = enc.Encode(Response{Result: json.RawMessage(raw)})
}

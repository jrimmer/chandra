package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
)

// ServerError represents an application-level error returned by the daemon in
// the response Error field. Callers can use errors.As to distinguish a daemon
// error from a network or protocol error.
type ServerError struct {
	Message string
}

func (e *ServerError) Error() string { return e.Message }

// Client connects to a chandrad daemon over a Unix socket and issues RPC calls.
type Client struct {
	socketPath string
}

// NewClient creates a Client that will connect to the given socketPath.
func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

// Call sends a request with the given method and params to the daemon,
// then decodes the response result into result. Each call opens a fresh
// connection to the socket and closes it when done.
//
// If the daemon returns a non-empty error string, Call returns that as an error.
// params may be nil. result may be nil (response is discarded).
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	// Establish connection, honouring context cancellation via a deadline if present.
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("api: connect to daemon: %w", err)
	}
	defer conn.Close()

	// Encode params into JSON for the request.
	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("api: marshal params: %w", err)
		}
	}

	req := Request{
		Method: method,
		Params: rawParams,
	}

	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("api: send request: %w", err)
	}

	var resp Response
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("api: decode response: %w", err)
	}

	if resp.Error != "" {
		return &ServerError{Message: resp.Error}
	}

	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("api: unmarshal result: %w", err)
		}
	}

	return nil
}

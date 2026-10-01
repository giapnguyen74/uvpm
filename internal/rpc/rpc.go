// Package rpc is a tiny newline-delimited JSON request/response protocol over
// a unix socket: one request and one response per connection.
package rpc

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// UnavailableError means the daemon could not be reached.
type UnavailableError struct{ Err error }

func (e *UnavailableError) Error() string { return "daemon not running: " + e.Err.Error() }
func (e *UnavailableError) Unwrap() error { return e.Err }

// RemoteError is an error returned by the daemon.
type RemoteError struct{ Msg string }

func (e *RemoteError) Error() string { return e.Msg }

// Call sends one request and decodes the result into result (may be nil).
func Call(sock, method string, params, result any) error {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return &UnavailableError{err}
	}
	defer conn.Close()
	req := Request{Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = b
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return &UnavailableError{err}
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("reading daemon response: %w", err)
	}
	if !resp.OK {
		return &RemoteError{resp.Error}
	}
	if result != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, result)
	}
	return nil
}

// Handler serves one method call.
type Handler func(method string, params json.RawMessage) (any, error)

// Serve accepts connections until the listener is closed.
func Serve(ln net.Listener, h Handler) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			var req Request
			if err := json.NewDecoder(conn).Decode(&req); err != nil {
				return
			}
			var resp Response
			res, err := h(req.Method, req.Params)
			if err != nil {
				resp.Error = err.Error()
			} else {
				resp.OK = true
				if res != nil {
					resp.Result, _ = json.Marshal(res)
				}
			}
			json.NewEncoder(conn).Encode(resp)
		}()
	}
}

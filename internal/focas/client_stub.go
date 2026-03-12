//go:build !cgo

// Package focas provides a FOCAS2 client for Fanuc CNC controllers.
// This stub is compiled when CGo is unavailable (CGO_ENABLED=0).
// All methods return an error so the caller can degrade gracefully.
package focas

import "errors"

var errNoCGo = errors.New("focas: CGo not available; rebuild with CGO_ENABLED=1 and the Fanuc FOCAS2 SDK")

// Client is a FOCAS2 session handle.
type Client struct{}

// Connect always returns an error in the stub build.
func Connect(host string, port int, timeoutSecs int) (*Client, error) {
	return nil, errNoCGo
}

// Close is a no-op in the stub build.
func (c *Client) Close() {}

// IsRunning always returns an error in the stub build.
func (c *Client) IsRunning() (bool, error) { return false, errNoCGo }

// ProgramName always returns an error in the stub build.
func (c *Client) ProgramName() (string, error) { return "", errNoCGo }

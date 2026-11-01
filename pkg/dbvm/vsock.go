package dbvm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

const (
	dialRetryInterval = 5 * time.Millisecond
	connectAckTimeout = 2 * time.Second
	statusPollEvery   = 5 * time.Millisecond
)

// Dial opens a stream to a guest vsock port through Firecracker's host
// AF_UNIX socket ("CONNECT <port>" handshake). It retries until ctx ends
// because the socket and the guest listener appear only as the VM boots.
func Dial(ctx context.Context, uds string, port uint32) (net.Conn, error) {
	var lastErr error
	for {
		conn, err := dialOnce(ctx, uds, port)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("dbvm: dial vsock port %d: %w", port, errors.Join(ctx.Err(), lastErr))
		case <-time.After(dialRetryInterval):
		}
	}
}

func dialOnce(ctx context.Context, uds string, port uint32) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", uds)
	if err != nil {
		return nil, err
	}
	if err := connectHandshake(conn, port); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func connectHandshake(conn net.Conn, port uint32) error {
	if err := conn.SetDeadline(time.Now().Add(connectAckTimeout)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		return err
	}
	// Read the ack byte by byte so no session bytes are buffered away.
	var ack strings.Builder
	buf := make([]byte, 1)
	for ack.Len() < 64 {
		if _, err := conn.Read(buf); err != nil {
			return err
		}
		if buf[0] == '\n' {
			break
		}
		ack.WriteByte(buf[0])
	}
	if !strings.HasPrefix(ack.String(), "OK ") {
		return fmt.Errorf("vsock CONNECT %d rejected: %q", port, ack.String())
	}
	return conn.SetDeadline(time.Time{})
}

// Control sends one control request and returns the guest's response.
func Control(ctx context.Context, uds string, req dbguest.Request) (dbguest.Response, error) {
	if err := req.Validate(); err != nil {
		return dbguest.Response{}, fmt.Errorf("dbvm: %w", err)
	}
	conn, err := Dial(ctx, uds, dbguest.ControlPort)
	if err != nil {
		return dbguest.Response{}, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return dbguest.Response{}, fmt.Errorf("dbvm: send %s: %w", req.Op, err)
	}
	// ReadBytes grows past the buffer, so the bound is the reader's.
	line, err := bufio.NewReader(io.LimitReader(conn, dbguest.MaxResponseBytes)).ReadBytes('\n')
	if err != nil {
		return dbguest.Response{}, fmt.Errorf("dbvm: read %s response: %w", req.Op, err)
	}
	var resp dbguest.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return dbguest.Response{}, fmt.Errorf("dbvm: decode %s response: %w", req.Op, err)
	}
	return resp, nil
}

// WaitState polls status until the guest reports one of want, or fails.
func WaitState(ctx context.Context, uds string, want ...dbguest.State) (dbguest.Response, error) {
	for {
		resp, err := Control(ctx, uds, dbguest.Request{Op: dbguest.OpStatus})
		if err != nil {
			return resp, err
		}
		for _, w := range want {
			if resp.State == w {
				return resp, nil
			}
		}
		if resp.State == dbguest.StateFailed {
			return resp, fmt.Errorf("dbvm: database failed: %s", resp.Error)
		}
		select {
		case <-ctx.Done():
			return resp, fmt.Errorf("dbvm: waiting for %v, last %s: %w", want, resp.State, ctx.Err())
		case <-time.After(statusPollEvery):
		}
	}
}

package sandboxproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

// Frame is one frame the host sent after its request.
type Frame struct {
	Type uint32
	Body []byte
}

// Handler runs one validated operation in the guest and returns its result,
// which Serve writes as the result frame. An error that is a *GuestError is
// written as its error frame; any other error as CodeInternal.
type Handler func(ctx context.Context, req Request, call *Call) (any, error)

// Call is the guest's side of one operation: the streams it writes and the
// frames the host sends while it runs.
type Call struct {
	conn    *Conn
	ctx     context.Context
	inbound chan Frame
}

// Serve reads one request from conn, runs handler and answers it. It closes
// conn when it returns.
func Serve(ctx context.Context, conn net.Conn, handler Handler) error {
	if handler == nil {
		return errors.New("sandbox protocol: nil handler")
	}
	c := NewConn(conn)
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	frameType, body, err := c.Read(ctx)
	if err != nil {
		return err
	}
	if frameType != FrameRequest {
		_ = c.WriteJSON(ctx, FrameError, ErrorFrame{Code: CodeInvalidRequest, Message: "the first frame must be a request"})
		return fmt.Errorf("%w: type %d before the request", ErrUnexpectedFrame, frameType)
	}
	var req Request
	if err := DecodeStrict(body, &req); err != nil {
		_ = c.WriteJSON(ctx, FrameError, ErrorFrame{Code: CodeInvalidRequest, Message: "the request is not valid JSON"})
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err := req.Validate(); err != nil {
		_ = c.WriteJSON(ctx, FrameError, ErrorFrame{Code: CodeInvalidRequest, Message: err.Error()})
		return err
	}
	call := &Call{conn: c, ctx: ctx, inbound: make(chan Frame, 16)}
	go call.readInbound()
	result, err := handler(ctx, req, call)
	if err != nil {
		var ge *GuestError
		if !errors.As(err, &ge) {
			ge = &GuestError{Code: CodeInternal}
		}
		return c.WriteJSON(ctx, FrameError, ErrorFrame{Code: ge.Code, Message: ge.Message})
	}
	return c.WriteJSON(ctx, FrameResult, result)
}

// readInbound queues the host's frames until the connection closes.
func (c *Call) readInbound() {
	defer close(c.inbound)
	for {
		frameType, body, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		select {
		case c.inbound <- Frame{Type: frameType, Body: body}:
		case <-c.ctx.Done():
			return
		}
	}
}

// Inbound delivers the frames the host sends after its request; it closes
// when the host closes the connection.
func (c *Call) Inbound() <-chan Frame { return c.inbound }

// Stdout, Stderr and Data write chunked stream frames.
func (c *Call) Stdout() io.Writer { return &frameStream{c: c, frameType: FrameStdout} }
func (c *Call) Stderr() io.Writer { return &frameStream{c: c, frameType: FrameStderr} }
func (c *Call) Data() io.Writer   { return &frameStream{c: c, frameType: FrameData} }

// ReadUpload copies the host's data frames to w until the data-end frame.
// More than max bytes is CodeTooLarge; a connection that closes first, or
// any other frame, ends the upload with an error.
func (c *Call) ReadUpload(w io.Writer, max int64) (int64, error) {
	var n int64
	for f := range c.inbound {
		switch f.Type {
		case FrameData:
			if n += int64(len(f.Body)); n > max {
				return n, &GuestError{Code: CodeTooLarge, Message: fmt.Sprintf("the upload is over %d bytes", max)}
			}
			if _, err := w.Write(f.Body); err != nil {
				return n, err
			}
		case FrameDataEnd:
			return n, nil
		default:
			return n, &GuestError{Code: CodeInvalidRequest, Message: fmt.Sprintf("frame type %d during an upload", f.Type)}
		}
	}
	return n, &GuestError{Code: CodeInvalidRequest, Message: "the upload ended without a data-end frame"}
}

type frameStream struct {
	c         *Call
	frameType uint32
}

func (s *frameStream) Write(p []byte) (int, error) {
	for sent := 0; sent < len(p); {
		end := min(sent+ChunkBytes, len(p))
		if err := s.c.conn.Write(s.c.ctx, s.frameType, p[sent:end]); err != nil {
			return sent, err
		}
		sent = end
	}
	return len(p), nil
}

// Client is the host's side of one operation.
type Client struct {
	conn *Conn
}

// Start sends req on conn.
func Start(ctx context.Context, conn net.Conn, req Request) (*Client, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	c := &Client{conn: NewConn(conn)}
	if err := c.conn.WriteJSON(ctx, FrameRequest, req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	return c, nil
}

// Send writes one frame while the operation runs: stdin, a resize, a kill.
func (c *Client) Send(ctx context.Context, frameType uint32, body []byte) error {
	return c.conn.Write(ctx, frameType, body)
}

// Upload streams r as data frames and ends them.
func (c *Client) Upload(ctx context.Context, r io.Reader) error {
	buf := make([]byte, ChunkBytes)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if werr := c.conn.Write(ctx, FrameData, buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return c.conn.Write(ctx, FrameDataEnd, nil)
		}
		if err != nil {
			return err
		}
	}
}

// Wait reads until the result, handing each stream and data frame to
// onFrame, and returns the result's JSON. An error frame is a *GuestError.
func (c *Client) Wait(ctx context.Context, onFrame func(frameType uint32, body []byte) error) (json.RawMessage, error) {
	for {
		frameType, body, err := c.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		switch frameType {
		case FrameStdout, FrameStderr, FrameData:
			if onFrame != nil {
				if err := onFrame(frameType, body); err != nil {
					return nil, err
				}
			}
		case FrameResult:
			return body, nil
		case FrameError:
			var e ErrorFrame
			if err := DecodeStrict(body, &e); err != nil {
				return nil, &GuestError{Code: CodeInternal, Message: "undecodable error frame"}
			}
			return nil, guestError(e)
		default:
			return nil, fmt.Errorf("%w: type %d", ErrUnexpectedFrame, frameType)
		}
	}
}

// Close closes the operation's connection.
func (c *Client) Close() error { return c.conn.Close() }

// Do runs one operation that takes no input and decodes its result into
// result, which may be nil.
func Do(ctx context.Context, conn net.Conn, req Request, onFrame func(uint32, []byte) error, result any) error {
	c, err := Start(ctx, conn, req)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	raw, err := c.Wait(ctx, onFrame)
	if err != nil || result == nil {
		return err
	}
	return DecodeStrict(raw, result)
}

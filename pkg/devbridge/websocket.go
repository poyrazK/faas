package devbridge

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const transportFrameBytes = 16 << 10

// WebSocketConn adapts a TLS-protected WebSocket into a byte stream for HTTP/2.
// HTTP/2 owns multiplexing, cancellation and per-stream flow control. Binary
// messages are transport chunks, never whole buffered application requests.
type WebSocketConn struct {
	socket  *websocket.Conn
	reader  io.Reader
	writeMu sync.Mutex
}

func NewWebSocketConn(socket *websocket.Conn) *WebSocketConn {
	socket.SetReadLimit(transportFrameBytes)
	return &WebSocketConn{socket: socket}
}

func (c *WebSocketConn) Read(p []byte) (int, error) {
	for {
		if c.reader == nil {
			kind, r, err := c.socket.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				return 0, errors.New("dev bridge requires binary transport frames")
			}
			c.reader = r
		}
		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *WebSocketConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > transportFrameBytes {
			n = transportFrameBytes
		}
		if err := c.socket.WriteMessage(websocket.BinaryMessage, p[:n]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func (c *WebSocketConn) Close() error         { return c.socket.Close() }
func (c *WebSocketConn) LocalAddr() net.Addr  { return c.socket.LocalAddr() }
func (c *WebSocketConn) RemoteAddr() net.Addr { return c.socket.RemoteAddr() }
func (c *WebSocketConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}
func (c *WebSocketConn) SetReadDeadline(t time.Time) error  { return c.socket.SetReadDeadline(t) }
func (c *WebSocketConn) SetWriteDeadline(t time.Time) error { return c.socket.SetWriteDeadline(t) }

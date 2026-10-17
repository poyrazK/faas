package liveserver

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
)

// WebSocket serves sessions over WebSocket connections, one session per
// connection. It is livequeryd's development transport; realtimed brings
// its own. checkOrigin decides which browser origins may connect.
func WebSocket(h *Hub, checkOrigin func(*http.Request) bool) http.Handler {
	return WebSocketTrusting(h, checkOrigin, nil)
}

// VerifiedPeer trusts a connection whose TLS client certificate the server
// verified: realtimed's, under livequeryd's -client-ca.
func VerifiedPeer(r *http.Request) bool { return r.TLS != nil && len(r.TLS.VerifiedChains) > 0 }

// WebSocketTrusting is WebSocket where trust decides which connections may
// present the verified claims of RLS-mode reads (nil trusts none).
func WebSocketTrusting(h *Hub, checkOrigin func(*http.Request) bool, trust func(*http.Request) bool) http.Handler {
	up := websocket.Upgrader{CheckOrigin: checkOrigin, ReadBufferSize: 16 << 10, WriteBufferSize: 64 << 10}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return // the upgrader has answered
		}
		ws.SetReadLimit(api.LiveQueryClientFrameMaxBytes)
		sess := h.attach(r.Context(), &wsConn{ws: ws}, trust != nil && trust(r))
		defer sess.Close(context.WithoutCancel(r.Context()))
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.TextMessage {
				sess.Receive(r.Context(), data)
			}
		}
	})
}

// wsConn is a session's WebSocket. Only the session writer sends; Close
// runs once.
type wsConn struct {
	ws   *websocket.Conn
	once sync.Once
}

func (c *wsConn) Send(frame []byte) error {
	if err := c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.TextMessage, frame)
}

func (c *wsConn) Close() error {
	var err error
	c.once.Do(func() {
		_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		err = c.ws.Close()
	})
	return err
}

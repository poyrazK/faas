// Package socket provides bounded transport over existing authenticated entity invocation.
package socket

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	MaxFrameBytes         = 64 << 10
	MaxConnections        = 64
	MaxAccountConnections = 16
	MaxEntityConnections  = 4
	Lifetime              = 10 * time.Minute
	IdleTimeout           = 30 * time.Second
	WriteTimeout          = 5 * time.Second
)

type Frame struct {
	ProtocolVersion int             `json:"protocol_version"`
	RequestID       string          `json:"request_id"`
	Payload         json.RawMessage `json:"payload"`
}
type Reply struct {
	ProtocolVersion int                              `json:"protocol_version"`
	RequestID       string                           `json:"request_id,omitempty"`
	Status          int                              `json:"status"`
	Result          *api.DurableEntityInvokeResponse `json:"result,omitempty"`
	Problem         json.RawMessage                  `json:"problem,omitempty"`
}
type Hub struct {
	mu                 sync.Mutex
	total              int
	accounts, entities map[string]int
}

func New() *Hub { return &Hub{accounts: map[string]int{}, entities: map[string]int{}} }
func (h *Hub) acquire(account, entity string) (func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= MaxConnections || h.accounts[account] >= MaxAccountConnections || h.entities[entity] >= MaxEntityConnections {
		return nil, false
	}
	h.total++
	h.accounts[account]++
	h.entities[entity]++
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.total--
		h.accounts[account]--
		h.entities[entity]--
		if h.accounts[account] == 0 {
			delete(h.accounts, account)
		}
		if h.entities[entity] == 0 {
			delete(h.entities, entity)
		}
	}, true
}

// Serve accepts Node/service clients only: browser Origin headers are rejected.
// One frame is committed and acknowledged before the next frame is read. There
// is no application queue, broadcast or socket state in the durability contract.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, account, entity string, invoke func(Frame) Reply) {
	if r.Header.Get("Origin") != "" {
		api.WriteProblem(w, api.ErrValidation("entity sockets accept authenticated service clients without an Origin header"))
		return
	}
	h.serve(w, r, account, entity, nil, invoke)
}

// ServeBrowser requires a previously authenticated grant and an exact Origin.
func (h *Hub) ServeBrowser(w http.ResponseWriter, r *http.Request, account, entity, origin string, invoke func(Frame) Reply) {
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != origin || !ValidOrigin(origin) {
		api.WriteProblem(w, api.ErrValidation("browser Origin does not match grant"))
		return
	}
	h.serve(w, r, account, entity, []string{BrowserProtocol}, invoke)
}
func (h *Hub) serve(w http.ResponseWriter, r *http.Request, account, entity string, protocols []string, invoke func(Frame) Reply) {
	release, ok := h.acquire(account, entity)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "durable_entity_socket_capacity", "Socket capacity reached", "retry connection later"))
		return
	}
	defer release()
	upgrader := websocket.Upgrader{Subprotocols: protocols, HandshakeTimeout: 5 * time.Second, CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	conn.SetReadLimit(MaxFrameBytes)
	deadline := time.Now().Add(Lifetime)
	for {
		readUntil := time.Now().Add(IdleTimeout)
		if deadline.Before(readUntil) {
			readUntil = deadline
		}
		if err := conn.SetReadDeadline(readUntil); err != nil {
			return
		}
		kind, body, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.TextMessage {
			return
		}
		var frame Frame
		// Unknown envelope fields are refused; payload belongs to the application.
		if err := decodeFrame(body, &frame); err != nil {
			return
		}
		reply := invoke(frame)
		reply.ProtocolVersion = 1
		reply.RequestID = frame.RequestID
		if err := conn.SetWriteDeadline(time.Now().Add(WriteTimeout)); err != nil {
			return
		}
		if err := conn.WriteJSON(reply); err != nil {
			return
		}
		if reply.Status == http.StatusUnauthorized || reply.Status == http.StatusForbidden || time.Now().After(deadline) {
			return
		}
	}
}

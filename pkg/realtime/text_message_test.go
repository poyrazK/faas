package realtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A text frame that is not UTF-8 makes every RFC 6455 client fail its socket,
// so one publish would disconnect a whole channel. The node refuses it and
// delivers the same bytes when they are marked binary.
func TestInvalidTextMessageRefusedBeforeFanout(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "ep"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+"ep", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(time.Second)
	for len(m.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snapshot := m.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("connections = %d, want 1", len(snapshot))
	}
	id := snapshot[0].ID
	if err := m.Subscribe(id, "news"); err != nil {
		t.Fatal(err)
	}
	invalid := []byte{0xff, 0xfe}
	tests := []struct {
		name string
		call func() error
	}{
		{"publish", func() error {
			_, err := m.PublishWithStatus(context.Background(), "ep", "news", Message{Data: invalid})
			return err
		}},
		{"send", func() error { return m.Send(context.Background(), id, Message{Data: invalid}) }},
	}
	for _, tt := range tests {
		if err := tt.call(); !errors.Is(err, ErrInvalidTextMessage) {
			t.Fatalf("%s invalid text: err = %v, want ErrInvalidTextMessage", tt.name, err)
		}
	}
	status, err := m.PublishWithStatus(context.Background(), "ep", "news", Message{Data: invalid, Binary: true})
	if err != nil || status.Queued != 1 {
		t.Fatalf("binary publish = %+v, %v; want one queued", status, err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, data, err := client.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(data) != string(invalid) {
		t.Fatalf("client read kind=%d data=%x err=%v; want the binary message and an open socket", kind, data, err)
	}
}

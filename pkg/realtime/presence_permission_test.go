package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// adr: 369 — reading a channel does not let a connection set presence state
// or send signals; the authorize callback is asked with permission
// "presence" (security review 2026-10-24, amendment of 2026-10-27).

type presencePermissionHooks struct {
	NopHooks
	mu          sync.Mutex
	presence    bool
	permissions []string
}

func (h *presencePermissionHooks) AuthorizeChannel(_ context.Context, event Event) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.permissions = append(h.permissions, event.Permission)
	return event.Permission != "presence" || h.presence, nil
}

func (h *presencePermissionHooks) asked(permission string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, got := range h.permissions {
		if got == permission {
			return true
		}
	}
	return false
}

// readResumeTestError reads frames until an error frame, failing after a few.
func readResumeTestError(t *testing.T, client interface {
	ReadMessage() (int, []byte, error)
	SetReadDeadline(time.Time) error
}) resumeServerFrame {
	t.Helper()
	for range 6 {
		_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := client.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var frame resumeServerFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type == "error" {
			return frame
		}
	}
	t.Fatal("no error frame")
	return resumeServerFrame{}
}

func resumeTestPresenceState(t *testing.T, m *Manager, channel string) string {
	t.Helper()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, member := range m.presenceMembers[channelKey{endpointID: "resume-endpoint", channel: channel}] {
		return string(member.state)
	}
	t.Fatalf("no presence member on %s", channel)
	return ""
}

func TestResumePresenceStateAndSignalsNeedPresencePermission(t *testing.T) {
	hooks := &presencePermissionHooks{}
	m, server := newResumeTestManager(t, &testResumeHistory{}, hooks)
	m.cfg.ResumePollInterval = time.Minute
	client := dialResumeTest(t, server)

	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates", State: json.RawMessage(`{"status":"online"}`)})
	if got := readResumeTestError(t, client); got.Code != "presence_not_authorized" || got.Channel != "updates" {
		t.Fatalf("subscribe with state = %+v", got)
	}
	if got := resumeTestPresenceState(t, m, "updates"); got != `{}` {
		t.Fatalf("joined with state %s, want {}", got)
	}
	if !hooks.asked("read") || !hooks.asked("presence") {
		t.Fatalf("callback permissions = %v", hooks.permissions)
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "presence", Channel: "updates", State: json.RawMessage(`{"status":"typing"}`)})
	if got := readResumeTestError(t, client); got.Code != "presence_not_authorized" {
		t.Fatalf("presence update = %+v", got)
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "signal", Channel: "updates", Data: json.RawMessage(`{"typing":true}`)})
	if got := readResumeTestError(t, client); got.Code != "presence_not_authorized" {
		t.Fatalf("signal = %+v", got)
	}
}

func TestResumePresencePermissionAllowsState(t *testing.T) {
	hooks := &presencePermissionHooks{presence: true}
	m, server := newResumeTestManager(t, &testResumeHistory{}, hooks)
	m.cfg.ResumePollInterval = time.Minute
	client := dialResumeTest(t, server)

	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates", State: json.RawMessage(`{"status":"online"}`)})
	if got := readResumeTestFrame(t, client); got.Type != "subscribed" {
		t.Fatalf("subscribe = %+v", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for resumeTestPresenceState(t, m, "updates") != `{"status":"online"}` {
		if time.Now().After(deadline) {
			t.Fatalf("presence state = %s", resumeTestPresenceState(t, m, "updates"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

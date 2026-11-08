package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// approvalServer gates POST /v1/apps/demo/park behind one approval request,
// whose GET answers come from statuses in order (the last repeats).
type approvalServer struct {
	mu        sync.Mutex
	statuses  []string
	note      string
	expiresAt time.Time
	posts     int
	keys      []string
	polls     int
}

func (s *approvalServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/park":
		s.posts++
		s.keys = append(s.keys, r.Header.Get("Idempotency-Key"))
		if s.posts > 1 && s.polls > 0 && s.statusLocked() == AgentApprovalApproved {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		p := NewProblem(http.StatusPreconditionRequired, CodeApprovalRequired, "Approval required", "a person must approve this request")
		p.AgentApproval = &AgentApprovalPendingInfo{ID: "ap-1", Category: AgentApprovalDelete, ReviewURL: "https://gregale.dev/approve/ap-1", ExpiresAt: s.expiresAt.UTC().Format(time.RFC3339)}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(p.Status)
		_ = json.NewEncoder(w).Encode(p)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/agent-approvals/ap-1":
		s.polls++
		_ = json.NewEncoder(w).Encode(AgentApprovalResponse{ID: "ap-1", Status: s.statusLocked(), Note: s.note})
	default:
		http.NotFound(w, r)
	}
}

func (s *approvalServer) statusLocked() string {
	if s.polls == 0 {
		return AgentApprovalPending
	}
	i := min(s.polls-1, len(s.statuses)-1)
	return s.statuses[i]
}

func TestWaitForApproval(t *testing.T) {
	tests := []struct {
		name      string
		wait      bool
		statuses  []string
		note      string
		expiresIn time.Duration
		wantCode  string
		wantPosts int
		wantNote  bool
	}{
		{name: "approved retries once", wait: true, statuses: []string{AgentApprovalPending, AgentApprovalApproved}, expiresIn: time.Minute, wantPosts: 2},
		{name: "denied stops with the note", wait: true, statuses: []string{AgentApprovalDenied}, note: "not on Friday", expiresIn: time.Minute, wantCode: CodeApprovalDenied, wantPosts: 1, wantNote: true},
		{name: "expired request", wait: true, statuses: []string{AgentApprovalExpired}, expiresIn: time.Minute, wantCode: CodeApprovalExpired, wantPosts: 1},
		{name: "deadline passes while pending", wait: true, statuses: []string{AgentApprovalPending}, expiresIn: 1500 * time.Millisecond, wantCode: CodeApprovalExpired, wantPosts: 1},
		{name: "no wait returns approval_required", wait: false, statuses: []string{AgentApprovalApproved}, expiresIn: time.Minute, wantCode: CodeApprovalRequired, wantPosts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &approvalServer{statuses: tt.statuses, note: tt.note, expiresAt: time.Now().Add(tt.expiresIn)}
			ts := httptest.NewServer(srv)
			defer ts.Close()
			c := NewClient(ts.URL, "agent-key")
			c.SetCompletionCache(nil)
			var notified []AgentApprovalPendingInfo
			if tt.wait {
				c.WaitForApproval(func(p AgentApprovalPendingInfo) { notified = append(notified, p) })
				c.approvalPoll = 10 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := c.do(ctx, http.MethodPost, "/v1/apps/demo/park", map[string]string{"reason": "test"}, nil)
			srv.mu.Lock()
			defer srv.mu.Unlock()
			if srv.posts != tt.wantPosts {
				t.Fatalf("posts = %d, want %d", srv.posts, tt.wantPosts)
			}
			if srv.posts == 2 && srv.keys[0] != srv.keys[1] {
				t.Fatalf("retry changed the Idempotency-Key: %q then %q", srv.keys[0], srv.keys[1])
			}
			if tt.wait && (len(notified) != 1 || notified[0].ReviewURL == "") {
				t.Fatalf("notified = %+v, want one request with its review link", notified)
			}
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Problem.Code != tt.wantCode {
				t.Fatalf("err = %v, want %s", err, tt.wantCode)
			}
			if tt.wantNote && apiErr.Problem.AgentApproval.Note != tt.note {
				t.Fatalf("note = %q, want %q", apiErr.Problem.AgentApproval.Note, tt.note)
			}
		})
	}
}

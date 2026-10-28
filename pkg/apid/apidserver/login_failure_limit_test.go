package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/state"
)

type loginNoticeMailer struct {
	mu   sync.Mutex
	sent []apidserver.Message
}

func (m *loginNoticeMailer) Send(_ context.Context, msg apidserver.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *loginNoticeMailer) messages() []apidserver.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]apidserver.Message(nil), m.sent...)
}

// A password guesser spread over many client addresses was limited only per
// address, 10 failures a minute each. The email address's own budget now
// refuses further password sign-ins, even with the right password, and tells
// the owner once (bug hunt 2026-10-28 H36).
func TestPasswordSignInLimitedPerEmailAcrossAddresses(t *testing.T) {
	const email, password = "guessed@example.com", "right-horse-battery-staple"
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	phc, err := auth.Encode(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPassword(ctx, acct.ID, phc); err != nil {
		t.Fatal(err)
	}
	mailer := &loginNoticeMailer{}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev",
		apidserver.NoopNotifier{}, "", mailer, nil, nil, nil, 15*time.Minute, "")
	h := srv.Handler()
	signIn := func(client int, email, password string) int {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: email, Password: password})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:4321", client)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range api.LoginFailuresPerEmail {
		if code := signIn(i+1, email, fmt.Sprintf("wrong-guess-%d", i)); code != http.StatusUnauthorized {
			t.Fatalf("guess %d: status %d, want 401", i, code)
		}
	}
	if code := signIn(200, "Guessed@Example.com", password); code != http.StatusTooManyRequests {
		t.Fatalf("sign-in after %d failures from other addresses: status %d, want 429", api.LoginFailuresPerEmail, code)
	}
	if code := signIn(201, "someone-else@example.com", "wrong-guess"); code != http.StatusUnauthorized {
		t.Fatalf("another address: status %d, want 401", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(mailer.messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sent := mailer.messages(); len(sent) != 1 || len(sent[0].To) != 1 || sent[0].To[0] != email {
		t.Fatalf("owner notices = %+v, want one to %s", sent, email)
	}
}

package apidserver_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// blockingMailer holds every Send until release is closed.
type blockingMailer struct {
	loginNoticeMailer
	release chan struct{}
}

func (m *blockingMailer) Send(ctx context.Context, msg apidserver.Message) error {
	<-m.release
	return m.loginNoticeMailer.Send(ctx, msg)
}

// A reset for a real account sent its mail on the request, so the response
// came back later for registered addresses than for unknown ones (bug hunt
// H86). The response must not wait for the mail.
func TestForgotPasswordAnswersBeforeTheMailIsSent(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "reset-timing@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	mailer := &blockingMailer{release: make(chan struct{})}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev",
		apidserver.NoopNotifier{}, "", mailer, nil, nil, nil, 15*time.Minute, "")
	h := srv.Handler()
	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/login/forgot", strings.NewReader("email=reset-timing%40example.com"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		done <- rec.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusOK {
			close(mailer.release)
			t.Fatalf("forgot status = %d", code)
		}
	case <-time.After(10 * time.Second):
		close(mailer.release)
		t.Fatal("forgot-password waited for the reset mail")
	}
	close(mailer.release)
	srv.WaitPasswordResetMail()
	if got := mailer.messages(); len(got) != 1 || got[0].To[0] != acct.Email {
		t.Fatalf("reset mail = %+v, want one to the account", got)
	}
}

// A full failure table stopped tracking new addresses, so filling it with
// random ones left the victim limited only per IP. The address with the most
// failures must survive the filler and still be refused.
func TestLoginFailureLimiterKeepsTargetedAddressWhenFull(t *testing.T) {
	l := apidserver.NewLoginFailureLimiterForTest()
	const victim = "victim-hash"
	for range api.LoginFailuresPerEmail - 1 {
		l.Record(victim)
	}
	for i := range api.LoginFailureEmailsTrackedMax + 1000 {
		l.Record(fmt.Sprintf("filler-%d", i))
	}
	l.Record(victim)
	if l.RetryAfter(victim) <= 0 {
		t.Fatal("targeted address forgotten once the table filled with random addresses")
	}
	if l.RetryAfter("never-seen") != 0 {
		t.Fatal("unknown address refused")
	}
}

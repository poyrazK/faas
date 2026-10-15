package queueleases

// adr: 1140

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type fakeHost struct {
	store *state.MemStore
	// wakes is handed to the lease as its notification stream; subscribed
	// reports that the lease is listening.
	wakes      chan db.Notification
	subscribed chan struct{}
}

func (h *fakeHost) Store() state.Store                           { return h.store }
func (h *fakeHost) WorkloadVerifier() *workloadidentity.Verifier { return nil }
func (h *fakeHost) Subscribe(_ context.Context, channels []string) (<-chan db.Notification, func(), error) {
	if len(channels) != 1 || channels[0] != db.NotifyInvocationDue {
		panic("unexpected channels")
	}
	close(h.subscribed)
	return h.wakes, func() {}, nil
}

type leaseFixture struct {
	host    *fakeHost
	account state.Account
	app     state.App
	binding state.QueueBinding
}

func newLeaseFixture(t *testing.T) leaseFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "wait@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "waiter", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs",
		Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	host := &fakeHost{store: store, wakes: make(chan db.Notification, 4), subscribed: make(chan struct{})}
	return leaseFixture{host: host, account: account, app: app, binding: binding}
}

// enqueue runs from helper goroutines, so it reports with t.Error.
func (f leaseFixture) enqueue(t *testing.T) {
	if _, err := f.host.store.EnqueueInvocation(context.Background(), state.Invocation{AccountID: f.account.ID, AppID: f.app.ID,
		Source: state.InvocationQueue, QueueName: "jobs", QueueBindingID: f.binding.ID, DueAt: time.Now().Add(-time.Millisecond),
		Payload: json.RawMessage(`{"n":1}`)}); err != nil {
		t.Error(err)
	}
}

// lease runs one lease call and returns its status and decoded body.
func (f leaseFixture) lease(t *testing.T, body string) (int, api.QueueLeaseResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/waiter/queues/jobs/leases", strings.NewReader(body))
	r.SetPathValue("slug", "waiter")
	r.SetPathValue("queue", "jobs")
	w := httptest.NewRecorder()
	Service{Host: f.host}.Lease(w, r, f.account, f.app, "")
	var resp api.QueueLeaseResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, resp
}

func TestLeaseWaitWakesOnQueueNotification(t *testing.T) {
	f := newLeaseFixture(t)
	go func() {
		<-f.host.subscribed
		// Another app's message must not end the wait.
		f.host.wakes <- db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"app_id":"other","source":"queue"}`}
		f.enqueue(t)
		f.host.wakes <- db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"app_id":"` + f.app.ID + `","source":"queue"}`}
	}()
	start := time.Now()
	code, resp := f.lease(t, `{"wait_seconds":10}`)
	if code != http.StatusOK || len(resp.Messages) != 1 {
		t.Fatalf("lease: status %d messages %d", code, len(resp.Messages))
	}
	if elapsed := time.Since(start); elapsed >= leaseRecheck {
		t.Fatalf("the notification did not wake the lease: took %s", elapsed)
	}
}

func TestLeaseWaitRechecksWithoutNotifications(t *testing.T) {
	f := newLeaseFixture(t)
	close(f.host.wakes) // a notifier that cannot listen
	go func() {
		<-f.host.subscribed
		time.Sleep(100 * time.Millisecond)
		f.enqueue(t)
	}()
	code, resp := f.lease(t, `{"wait_seconds":5}`)
	if code != http.StatusOK || len(resp.Messages) != 1 {
		t.Fatalf("lease: status %d messages %d", code, len(resp.Messages))
	}
}

func TestLeaseWaitEndsEmpty(t *testing.T) {
	f := newLeaseFixture(t)
	start := time.Now()
	code, resp := f.lease(t, `{"wait_seconds":1}`)
	if code != http.StatusOK || len(resp.Messages) != 0 {
		t.Fatalf("lease: status %d messages %d", code, len(resp.Messages))
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("lease returned after %s, before its 1 s wait", elapsed)
	}
}

func TestLeaseWaitValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"no wait returns at once", `{}`, http.StatusOK},
		{"negative wait", `{"wait_seconds":-1}`, http.StatusBadRequest},
		{"wait past the ceiling", `{"wait_seconds":21}`, http.StatusBadRequest},
		{"batch past the ceiling", `{"max_messages":101}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := newLeaseFixture(t).lease(t, tc.body); code != tc.want {
				t.Fatalf("status %d, want %d", code, tc.want)
			}
		})
	}
}

func TestQueueMessageDue(t *testing.T) {
	for _, tc := range []struct {
		payload string
		want    bool
	}{
		{`{"invocation_id":"i","app_id":"a","source":"queue"}`, true},
		{`{"app_id":"a","source":"async_invoke"}`, false},
		{`{"app_id":"b","source":"queue"}`, false},
		{`not json`, false},
	} {
		if got := queueMessageDue(tc.payload, "a"); got != tc.want {
			t.Errorf("queueMessageDue(%s) = %v, want %v", tc.payload, got, tc.want)
		}
	}
}

// A nack's error is cut to errorMaxBytes on a rune boundary: Postgres
// rejects the invalid UTF-8 a byte cut through "é" would store.
func TestNackTruncatesErrorOnRuneBoundary(t *testing.T) {
	f := newLeaseFixture(t)
	f.enqueue(t)
	code, leased := f.lease(t, `{}`)
	if code != http.StatusOK || len(leased.Messages) != 1 {
		t.Fatalf("lease: status %d, %d messages", code, len(leased.Messages))
	}
	msg := leased.Messages[0]
	body, err := json.Marshal(api.QueueLeaseReceiptsRequest{Receipts: []string{msg.Receipt},
		Error: strings.Repeat("x", errorMaxBytes-1) + "é tail"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/waiter/queues/jobs/leases/nack", strings.NewReader(string(body)))
	r.SetPathValue("slug", "waiter")
	r.SetPathValue("queue", "jobs")
	w := httptest.NewRecorder()
	Service{Host: f.host}.Nack(w, r, f.account, f.app, "")
	if w.Code != http.StatusOK {
		t.Fatalf("nack: status %d, body %s", w.Code, w.Body.String())
	}
	inv, err := f.host.store.InvocationByID(context.Background(), msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(inv.LastError) || len(inv.LastError) != errorMaxBytes-1 {
		t.Errorf("stored error: %d bytes, valid UTF-8 %v; want the %d bytes before the split rune",
			len(inv.LastError), utf8.ValidString(inv.LastError), errorMaxBytes-1)
	}
}

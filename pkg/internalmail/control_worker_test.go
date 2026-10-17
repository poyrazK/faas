package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type controlBlockingCodec struct {
	Codec
	ctx     context.Context
	entered chan struct{}
	release chan struct{}
}

func (c controlBlockingCodec) Open(resource string, data []byte) ([]byte, error) {
	if strings.HasPrefix(resource, "message:") {
		close(c.entered)
		select {
		case <-c.release:
		case <-c.ctx.Done():
			return nil, c.ctx.Err()
		}
	}
	return c.Codec.Open(resource, data)
}

type controlTransportFunc func(context.Context, Envelope) (string, string)

func (f controlTransportFunc) Submit(ctx context.Context, e Envelope) (string, string) {
	return f(ctx, e)
}

func TestPGCancellationDuringRenderFencesSMTPAndWorkerSettlement(t *testing.T) {
	f := controlsFixture(t)
	m := singleMail(t, f, "cancel-during-render")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.service.Codec = controlBlockingCodec{f.service.Codec, ctx, entered, release}
	f.delivery.Store(true)
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never entered render")
	}
	if result := cancellation(t, f, m.ID, true); !result.Complete || len(result.Cancelled) != 1 {
		t.Fatal("claimed work could not be cancelled")
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("expected cancellation race reported a worker failure", err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled worker did not settle")
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "cancelled" || len(f.transport.envelopes) != 0 {
		t.Fatal("cancelled claim was revived or submitted", err)
	}
}

func TestPGCancellationDuringSMTPPreservesSubmissionAndReports(t *testing.T) {
	f := controlsFixture(t)
	m := singleMail(t, f, "cancel-during-smtp")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.service.Transport = controlTransportFunc(func(ctx context.Context, _ Envelope) (string, string) {
		close(entered)
		select {
		case <-release:
			return "submitted", "relay_accepted"
		case <-ctx.Done():
			return "uncertain", "interrupted"
		}
	})
	f.delivery.Store(true)
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never entered SMTP")
	}
	result := cancellation(t, f, m.ID, true)
	if result.Complete || len(result.Cancelled) != 0 || len(result.Protected) != 1 || result.Protected[0].Status != "submitting" {
		t.Fatal("cancellation interrupted SMTP custody")
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("submitted worker did not settle")
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: m.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal("protected submission lost report correlation", err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "accepted" {
		t.Fatal("report did not advance protected submission")
	}
}

func TestPGCancellationMixedCustodyAndDailyBudget(t *testing.T) {
	f := controlsFixture(t)
	store, err := NewPGStoreWithDailyLimit(f.pool, "platform", 3)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = store
	m := submit(t, f, "mixed-cancellation", request())
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := cancellation(t, f, m.ID, true)
	if result.Complete || len(result.Cancelled) != 1 || len(result.Protected) != 1 || result.Protected[0].Status != "submitted" {
		t.Fatal("mixed custody cancellation lost its partial result")
	}
	if _, err := f.service.Submit(t.Context(), "new-after-cancel", request()); !errors.Is(err, ErrQuota) {
		t.Fatal("cancellation refunded the daily admission budget", err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("partial cancellation allowed another SMTP handoff", err)
	}
}

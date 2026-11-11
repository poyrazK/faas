package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// emailRevertBackend lists only the Store methods this test calls.
type emailRevertBackend interface {
	accountFlowBackend
	state.AccountEmailRevertStore
}

// An operator revert moves the account back from the address its last
// confirmed change moved it to, once, spends links issued meanwhile, and
// refuses when the previous address now belongs to someone else
// (ADR 20261110100000).
func TestRevertLastEmailChange(t *testing.T) {
	for name, open := range accountFlowBackends(t) {
		t.Run(name, func(t *testing.T) {
			s := open(t).(emailRevertBackend)
			ctx := t.Context()
			a, err := s.CreateAccount(ctx, "revert-owner@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RevertLastEmailChange(ctx, a.ID, time.Hour); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("revert without a change = %v, want ErrNotFound", err)
			}
			if err := s.IssueEmailChange(ctx, flowToken("revert-1"), a.ID, "revert-taker@example.net", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ConsumeEmailChange(ctx, flowToken("revert-1")); err != nil {
				t.Fatal(err)
			}
			// The taker asks for a reset link at their address.
			if err := s.IssueLoginToken(ctx, flowToken("taker-reset"), a.ID, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RevertLastEmailChange(ctx, a.ID, time.Nanosecond); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("revert outside the window = %v, want ErrNotFound", err)
			}

			change, err := s.RevertLastEmailChange(ctx, a.ID, time.Hour)
			if err != nil || change.OldEmail != "revert-owner@example.com" || change.NewEmail != "revert-taker@example.net" {
				t.Fatalf("revert = %+v, %v", change, err)
			}
			after, err := s.AccountByID(ctx, a.ID)
			if err != nil || after.Email != "revert-owner@example.com" || !after.EmailVerified() {
				t.Fatalf("after revert email=%q verified=%v err=%v", after.Email, after.EmailVerified(), err)
			}
			if _, err := s.ConsumeLoginToken(ctx, flowToken("taker-reset")); err == nil {
				t.Error("a reset link issued to the taker's address survived the revert")
			}
			if _, err := s.RevertLastEmailChange(ctx, a.ID, time.Hour); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second revert = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestRevertLastEmailChangeRefusesTakenAddress(t *testing.T) {
	for name, open := range accountFlowBackends(t) {
		t.Run(name, func(t *testing.T) {
			s := open(t).(emailRevertBackend)
			ctx := t.Context()
			a, err := s.CreateAccount(ctx, "revert-gone@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.IssueEmailChange(ctx, flowToken("revert-2"), a.ID, "revert-moved@example.net", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ConsumeEmailChange(ctx, flowToken("revert-2")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateAccount(ctx, "revert-gone@example.com", api.PlanFree); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RevertLastEmailChange(ctx, a.ID, time.Hour); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("revert onto a taken address = %v, want ErrConflict", err)
			}
			if after, err := s.AccountByID(ctx, a.ID); err != nil || after.Email != "revert-moved@example.net" {
				t.Fatalf("a refused revert changed the email to %q (err %v)", after.Email, err)
			}
		})
	}
}

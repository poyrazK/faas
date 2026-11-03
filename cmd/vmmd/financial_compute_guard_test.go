package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAppAdmissionGuardReadsFenceAndCapsAtOnce(t *testing.T) {
	errFenced, errCapped := errors.New("fenced"), errors.New("capped")
	for _, tc := range []struct {
		name            string
		fence, capCheck error
		want            error
	}{
		{name: "admitted"},
		{name: "fence answers first", fence: errFenced, capCheck: errCapped, want: errFenced},
		{name: "cap refuses", capCheck: errCapped, want: errCapped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each read waits for the other to start: run one after
			// another, the guard would time out.
			fenceStarted, capStarted := make(chan struct{}), make(chan struct{})
			wait := func(ch chan struct{}) error {
				select {
				case <-ch:
					return nil
				case <-time.After(5 * time.Second):
					return errors.New("reads ran one after another")
				}
			}
			base := func(context.Context, string) error {
				close(fenceStarted)
				if err := wait(capStarted); err != nil {
					return err
				}
				return tc.fence
			}
			bypass := func(context.Context, string) error {
				close(capStarted)
				if err := wait(fenceStarted); err != nil {
					return err
				}
				return tc.capCheck
			}
			if err := appAdmissionGuard(base, bypass)(t.Context(), "app"); !errors.Is(err, tc.want) {
				t.Fatalf("guard = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAppAdmissionGuardWithoutFenceIsTheCapCheck(t *testing.T) {
	errCapped := errors.New("capped")
	guard := appAdmissionGuard(nil, func(context.Context, string) error { return errCapped })
	if err := guard(t.Context(), "app"); !errors.Is(err, errCapped) {
		t.Fatalf("guard = %v, want %v", err, errCapped)
	}
}

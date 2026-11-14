package mcphosting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestDelegatedWorkerPreservesDispatchAndUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		mode         int32
		calls        int32
		wantError    bool
	}{
		{"catalog drift", "not_started", 1, 0, false},
		{"pin unavailable", "not_started", 0, 0, false},
		{"lost reply", "outcome_unknown", 2, 1, false},
		{"tool error", "tool_error", 3, 1, false},
		{"lost dispatch receipt", "running", 0, 0, true},
		{"cancel running", "outcome_unknown", 4, 1, false},
		{"revoke before dispatch", "queued", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := granttest.New(t, state.NewMemStore(), "")
			a := granttest.NewRuntime(t, f)
			a.Mode.Store(tc.mode)
			grantID, id := uuid.NewString(), uuid.NewString()
			_, err := f.Owner.PutToolGrant(t.Context(), grantID, api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Agent.BeginDelegatedToolInvocation(t.Context(), id, api.DelegatedToolInvocationIntent{GrantID: grantID, Intent: api.ToolInvocationIntent{Version: f.PublicationA.ID, Name: "visible", ArgumentsSHA256: toolpublication.Digest([]byte(`{}`)), AccessMode: "open", AllowToolExecution: true, TimeoutSeconds: 10}})
			if err != nil {
				t.Fatal(err)
			}
			f.LoseDispatch = tc.name == "lost dispatch receipt"
			opts := mcphosting.RegisteredInvocationOptions{Delegated: true, Native: f.Agent, ID: id, Arguments: []byte(`{}`), HTTP: a.Server.Client(), VerifyPin: func(ctx context.Context, _ api.ToolInvocationTarget, _ string) error {
				if tc.name == "pin unavailable" {
					return errors.New("pin unavailable")
				}
				if tc.name == "revoke before dispatch" {
					_, revokeErr := f.Owner.RevokeToolGrant(ctx, grantID)
					return revokeErr
				}
				return nil
			}}
			done := make(chan error, 1)
			go func() { _, runErr := mcphosting.RunRegisteredInvocation(t.Context(), opts); done <- runErr }()
			if tc.name == "cancel running" {
				select {
				case <-a.Started:
				case <-time.After(5 * time.Second):
					t.Fatal("tool did not start")
				}
				if _, err = f.Agent.CancelDelegatedToolInvocation(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case runErr := <-done:
				if (runErr != nil) != tc.wantError {
					t.Fatal("worker outcome", runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not stop")
			}
			record, err := f.Owner.GetToolInvocation(t.Context(), id)
			if err != nil || record.Status != tc.status || a.Calls.Load() != tc.calls {
				t.Fatalf("retained state=%s calls=%d error=%v", record.Status, a.Calls.Load(), err)
			}
			if tc.name != "revoke before dispatch" {
				if _, err = mcphosting.RunRegisteredInvocation(t.Context(), opts); err != nil || a.Calls.Load() != tc.calls {
					t.Fatal("recovered worker repeated effects", err)
				}
			}
		})
	}
}

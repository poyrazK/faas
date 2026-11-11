package receivers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeAgentRequests struct {
	allow   bool
	id      string
	problem *api.Problem
	err     error
	binding state.AgentApprovalBinding
	row     state.AgentActivity
}

func (f *fakeAgentRequests) DecideAgentRequest(_ context.Context, _, _, _ string, binding state.AgentApprovalBinding, _ string) (bool, string, *api.Problem, error) {
	f.binding = binding
	return f.allow, f.id, f.problem, f.err
}

func (f *fakeAgentRequests) RecordAgentActivity(_ context.Context, row state.AgentActivity) error {
	f.row = row
	return f.err
}

// ADR-20261111150000: the Auth service answers a gateway's agent calls with
// apid's gate, carries the problem as RFC 7807 JSON, and maps a foreign key
// to NotFound and a store failure to Unavailable.
func TestAuthReceiver_DecideAgentRequest(t *testing.T) {
	req := &apidpb.DecideAgentRequestRequest{AccountId: "acct", KeyId: "key", Pattern: "GET /v1/apps/{slug}/debug", Method: "GET", Path: "/v1/apps/a/debug"}
	if _, err := newAuthReceiver(nil, nil, nil).DecideAgentRequest(t.Context(), req); status.Code(err) != codes.Unavailable {
		t.Fatalf("without agents = %v; want Unavailable", err)
	}
	required := api.NewProblem(http.StatusPreconditionRequired, api.CodeApprovalRequired, "Approval required", "needs a person")
	fake := &fakeAgentRequests{problem: required}
	r := newAuthReceiver(nil, nil, nil, WithAgentRequests(fake))
	if _, err := r.DecideAgentRequest(t.Context(), &apidpb.DecideAgentRequestRequest{KeyId: "key"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing fields = %v; want InvalidArgument", err)
	}
	resp, err := r.DecideAgentRequest(t.Context(), req)
	var problem api.Problem
	if err != nil || resp.GetAllow() || json.Unmarshal(resp.GetProblem(), &problem) != nil || problem.Status != http.StatusPreconditionRequired || problem.Code != api.CodeApprovalRequired {
		t.Fatalf("refusal = %+v %v", resp, err)
	}
	if fake.binding.KeyID != "key" || fake.binding.Path != "/v1/apps/a/debug" {
		t.Errorf("binding = %+v", fake.binding)
	}
	*fake = fakeAgentRequests{allow: true, id: "ap-1"}
	if resp, err := r.DecideAgentRequest(t.Context(), req); err != nil || !resp.GetAllow() || resp.GetApprovalId() != "ap-1" || len(resp.GetProblem()) != 0 {
		t.Fatalf("allowed = %+v %v", resp, err)
	}
	for want, cause := range map[codes.Code]error{codes.NotFound: state.ErrNotFound, codes.Unavailable: errors.New("db down")} {
		*fake = fakeAgentRequests{err: cause}
		if _, err := r.DecideAgentRequest(t.Context(), req); status.Code(err) != want {
			t.Errorf("%v = %v; want %v", cause, err, want)
		}
		if _, err := r.RecordAgentActivity(t.Context(), &apidpb.RecordAgentActivityRequest{AccountId: "acct", KeyId: "key", Method: "GET", Path: "/p", Status: 101}); status.Code(err) != want {
			t.Errorf("record %v = %v; want %v", cause, err, want)
		}
	}
}

func TestAuthReceiver_RecordAgentActivity(t *testing.T) {
	fake := &fakeAgentRequests{}
	r := newAuthReceiver(nil, nil, nil, WithAgentRequests(fake))
	if _, err := r.RecordAgentActivity(t.Context(), &apidpb.RecordAgentActivityRequest{AccountId: "acct", KeyId: "key", Method: "GET", Path: "/p"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no status = %v; want InvalidArgument", err)
	}
	_, err := r.RecordAgentActivity(t.Context(), &apidpb.RecordAgentActivityRequest{
		AccountId: "acct", KeyId: "key", Method: "GET", Path: "/v1/apps/a/debug", Status: 101, ApprovalId: "ap-1", CreatedAtUnixMs: 1_700_000_000_000,
	})
	if err != nil || fake.row.Status != 101 || fake.row.ApprovalID != "ap-1" || fake.row.CreatedAt.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("recorded %+v, %v", fake.row, err)
	}
}

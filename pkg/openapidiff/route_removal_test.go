package openapidiff

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type approvedRemovalStore struct {
	*state.MemStore
	approval api.RouteRemovalCheck
	docs     map[string][]byte
}

func (s *approvedRemovalStore) CheckRouteRemoval(context.Context, string, string, string) (api.RouteRemovalCheck, error) {
	return s.approval, nil
}
func (s *approvedRemovalStore) GetDeploymentOpenAPIDoc(_ context.Context, id, account string) ([]byte, state.OpenAPIDocMeta, error) {
	doc := s.docs[id]
	hash := sha256.Sum256(doc)
	return doc, state.OpenAPIDocMeta{DocSHA256: hash[:]}, nil
}

func TestApplyRemovalException(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	account, err := mem.CreateAccount(ctx, "removal@test.invalid", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "removal", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	baseline := []byte(`{"openapi":"3.0.3","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	candidate := []byte(`{"openapi":"3.0.3","paths":{"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	b, _, err := SnapshotFromDocument("baseline", app.ID, "prod", baseline, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := SnapshotFromDocument("candidate", app.ID, "prod", candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := CompareSnapshots(b.Snapshot, c.Snapshot)
	if err != nil || len(diff.Breaks) != 1 {
		t.Fatalf("removed diff: %+v %v", diff, err)
	}
	bsha, csha := sha256.Sum256(baseline), sha256.Sum256(candidate)
	approved := api.RouteRemovalCheck{Status: "passed", ApprovalID: "approval", Policy: api.RouteRemovalPolicy{Mode: "enforce", Revision: 1, BaselineDeploymentID: b.DeploymentID}, Removed: []api.RouteRemovalMapping{{Method: "GET", Path: "/old"}}, BaselineContractSHA256: fmt.Sprintf("%x", bsha), CandidateContractSHA256: fmt.Sprintf("%x", csha)}
	for _, name := range []string{"approved", "report", "blocked", "wrong_baseline", "changed_capture", "other_break", "unknown", "dark"} {
		t.Run(name, func(t *testing.T) {
			store := &approvedRemovalStore{MemStore: mem, approval: approved, docs: map[string][]byte{b.DeploymentID: baseline, c.DeploymentID: candidate}}
			check := PromotionCheck{Baseline: b, Proposed: c, HasBaseline: true, Diff: diff}
			switch name {
			case "report":
				store.approval.Policy.Mode = "report"
			case "blocked":
				store.approval.Status = "blocked"
			case "wrong_baseline":
				store.approval.Policy.BaselineDeploymentID = "different"
			case "changed_capture":
				store.docs[c.DeploymentID] = baseline
			case "other_break":
				check.Diff.Breaks = append(append([]SchemaBreak{}, diff.Breaks...), SchemaBreak{Path: "/new", Method: "get", Status: "200", Kind: SchemaKindRequiredRemoved, PathInSchema: "id"})
			case "unknown":
				check.Diff.Unknowns = []SchemaUnknown{{Path: "/new", Method: "get"}}
			}
			result, fence, err := ApplyRemovalException(ctx, store, check, name == "dark")
			if err != nil {
				t.Fatal(err)
			}
			allowed := name == "approved" || name == "dark"
			if result.Diff.Blocking() == allowed {
				t.Fatalf("allowed=%v: %+v", allowed, result.Diff)
			}
			if (fence != nil) != (name == "approved" || name == "other_break" || name == "unknown") {
				t.Fatalf("unexpected fence: %+v", fence)
			}
			if name == "other_break" && (len(result.Diff.Breaks) != 1 || result.Diff.Breaks[0].Kind != SchemaKindRequiredRemoved) {
				t.Fatalf("waived unrelated break: %+v", result.Diff)
			}
			if name == "unknown" && len(result.Diff.Unknowns) != 1 {
				t.Fatal("waived unknown")
			}
		})
	}
}

package bindingrun

import (
	"context"
	"errors"
	"github.com/poyrazK/faas/sdk/go/internal/api"
	"testing"
	"time"
)

type creationFake struct {
	CreationClient
	app           api.BindingVerificationRun
	graph         api.ProjectBindingVerificationRun
	err           error
	writes, reads int
	request       api.CreateBindingVerificationRunRequest
	graphRequest  api.CreateProjectBindingVerificationRunRequest
}

func (f *creationFake) CreateBindingVerificationRun(_ context.Context, _ string, r api.CreateBindingVerificationRunRequest) (api.BindingVerificationRun, error) {
	f.writes++
	f.request = r
	return f.app, f.err
}
func (f *creationFake) GetBindingVerificationRun(_ context.Context, _, _ string) (api.BindingVerificationRun, error) {
	f.reads++
	return f.app, f.err
}
func (f *creationFake) CreateProjectBindingVerificationRun(_ context.Context, _, _ string, r api.CreateProjectBindingVerificationRunRequest) (api.ProjectBindingVerificationRun, error) {
	f.writes++
	f.graphRequest = r
	return f.graph, f.err
}
func (f *creationFake) GetProjectBindingVerificationRun(_ context.Context, _, _, _ string) (api.ProjectBindingVerificationRun, error) {
	f.reads++
	return f.graph, f.err
}
func creationFixture(graph bool) (*creationFake, api.BindingVerificationReview, api.ProjectBindingVerificationReview, CreationOptions) {
	f := newFake(contractCase{Graph: graph, Actions: []string{"task", "queue-canary"}})
	review := func(r api.BindingVerificationRun) api.BindingVerificationReview {
		r.Inventory.VerificationDeploymentID = r.DeploymentID
		plan := []api.BindingVerificationPlanStep{}
		for _, s := range r.Steps {
			plan = append(plan, s.Plan)
		}
		return api.BindingVerificationReview{App: r.App, DeploymentID: r.DeploymentID, Scope: r.Scope, ReviewedAt: time.Now().Add(-time.Second), Ready: true, ReviewDigest: r.ReviewDigest, Inventory: r.Inventory, Policy: r.Policy, Plan: plan}
	}
	c := &creationFake{}
	o := CreationOptions{TTLSeconds: 60, QueuePublish: true}
	if !graph {
		c.app = *f.root.app
		r := review(c.app)
		c.app.Inventory = r.Inventory
		o.RunID = c.app.ID
		return c, r, api.ProjectBindingVerificationReview{}, o
	}
	c.graph = *f.root.graph
	o.RunID = c.graph.ID
	g := api.ProjectBindingVerificationReview{Project: c.graph.Project, Environment: c.graph.Environment, ReviewedAt: time.Now().Add(-time.Second), Ready: true, ReviewDigest: c.graph.ReviewDigest, Manifest: c.graph.Manifest, QueuePublish: true, Members: map[string]api.BindingVerificationReview{}, ReviewDigests: map[string]string{}}
	for n, m := range c.graph.Members {
		r := review(m)
		m.Inventory = r.Inventory
		c.graph.Members[n] = m
		g.Members[n] = r
		g.ReviewDigests[n] = r.ReviewDigest
	}
	return c, api.BindingVerificationReview{}, g, o
}
func TestReviewedCreationRecovery(t *testing.T) {
	for _, graph := range []bool{false, true} {
		t.Run(map[bool]string{false: "app", true: "graph"}[graph], func(t *testing.T) {
			c, r, g, o := creationFixture(graph)
			lost := errors.New("create response lost")
			c.err = lost
			var err error
			if graph {
				_, err = CreateProject(context.Background(), c, "project", "production", g, o)
			} else {
				_, err = CreateApp(context.Background(), c, "api", r, o)
			}
			var recovery *CreationError
			if !errors.As(err, &recovery) || !errors.Is(err, lost) || !recovery.Attempted || recovery.RunID != o.RunID || c.writes != 1 || c.reads != 0 {
				t.Fatalf("lost response identity: %v %+v", err, c)
			}
			c.err = nil
			if graph {
				_, err = ReconcileProject(context.Background(), c, "project", "production", g, o)
				if c.graphRequest.ReviewDigest != g.ReviewDigest || c.graphRequest.ReviewDigests["api"] != g.Members["api"].ReviewDigest {
					t.Fatal("digests changed")
				}
			} else {
				_, err = ReconcileApp(context.Background(), c, "api", r, o)
				if c.request.ReviewDigest != r.ReviewDigest {
					t.Fatal("digest changed")
				}
			}
			if err != nil || c.writes != 1 || c.reads != 1 {
				t.Fatalf("reconcile: %v", err)
			}
		})
	}
}
func TestReviewedCreationRejectsSelectionAndConsent(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, fault := range []string{"uuid", "consent", "ready", "digest", "scope", "member", "ttl", "budget", "plan", "deadline"} {
			t.Run(map[bool]string{false: "app", true: "graph"}[graph]+"/"+fault, func(t *testing.T) {
				c, r, g, o := creationFixture(graph)
				local := true
				switch fault {
				case "uuid":
					o.RunID = "bad"
				case "consent":
					o.QueuePublish = false
					r.QueuePublishRequired = true
				case "ready":
					r.Ready = false
					g.Ready = false
				case "digest":
					r.ReviewDigest = "bad"
					g.ReviewDigest = "bad"
				case "scope":
					r.Scope = "other"
					g.Environment = "other"
				case "member":
					c.app.DeploymentID = "changed"
					delete(c.graph.Members, "billing")
					local = false
				case "ttl":
					o.TTLSeconds = 0
				case "budget":
					o.MaxConcurrency = 9
				case "plan":
					c.app.Steps = nil
					if graph {
						c.graph.Members["api"] = api.BindingVerificationRun{}
					}
					local = false
				case "deadline":
					c.app.ExpiresAt = c.app.ExpiresAt.Add(time.Second)
					c.graph.ExpiresAt = c.graph.ExpiresAt.Add(time.Second)
					local = false
				}
				var err error
				if graph {
					_, err = CreateProject(context.Background(), c, "project", "production", g, o)
				} else {
					_, err = CreateApp(context.Background(), c, "api", r, o)
				}
				var recovery *CreationError
				if !errors.As(err, &recovery) || recovery.RunID != o.RunID || recovery.Attempted == local || local && c.writes != 0 || c.reads != 0 {
					t.Fatalf("selection accepted or wrong error: %v %+v", err, c)
				}
			})
		}
	}
}

func TestReviewedCreationCancelledAndTerminalReconciliation(t *testing.T) {
	for _, graph := range []bool{false, true} {
		c, r, g, o := creationFixture(graph)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var err error
		if graph {
			_, err = CreateProject(ctx, c, "project", "production", g, o)
		} else {
			_, err = CreateApp(ctx, c, "api", r, o)
		}
		var failure *CreationError
		if !errors.As(err, &failure) || failure.Attempted || !errors.Is(err, context.Canceled) || c.writes != 0 {
			t.Fatalf("cancelled creation: %v", err)
		}
		past := time.Now().Add(-time.Hour)
		if graph {
			c.graph.CreatedAt = past
			c.graph.ExpiresAt = past.Add(time.Minute)
			c.graph.Status = "cancelled"
			for n, m := range c.graph.Members {
				m.CreatedAt = c.graph.CreatedAt
				m.ExpiresAt = c.graph.ExpiresAt
				m.Status = "cancelled"
				c.graph.Members[n] = m
			}
			_, err = ReconcileProject(context.Background(), c, "project", "production", g, o)
		} else {
			c.app.CreatedAt = past
			c.app.ExpiresAt = past.Add(time.Minute)
			c.app.Status = "cancelled"
			_, err = ReconcileApp(context.Background(), c, "api", r, o)
		}
		if err != nil || c.reads != 1 || c.writes != 0 {
			t.Fatalf("terminal reconciliation: %v", err)
		}
		if graph {
			c.graph.ReviewDigest = "changed"
			_, err = ReconcileProject(context.Background(), c, "project", "production", g, o)
		} else {
			c.app.ReviewDigest = "changed"
			_, err = ReconcileApp(context.Background(), c, "api", r, o)
		}
		if err == nil || c.reads != 2 || c.writes != 0 {
			t.Fatalf("mismatched reconciliation accepted: %v", err)
		}
	}
}

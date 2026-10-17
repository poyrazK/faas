package bindingrun

import (
	"context"
	"errors"
	"github.com/poyrazK/faas/sdk/go/internal/api"
	"io"
	"testing"
	"time"
)

type createRecoveryFake struct {
	*creationFake
	cause error
	mode  string
}

func (f *createRecoveryFake) CreateBindingVerificationRun(ctx context.Context, app string, r api.CreateBindingVerificationRunRequest) (api.BindingVerificationRun, error) {
	f.writes++
	f.request = r
	return api.BindingVerificationRun{}, f.cause
}
func (f *createRecoveryFake) CreateProjectBindingVerificationRun(ctx context.Context, p, env string, r api.CreateProjectBindingVerificationRunRequest) (api.ProjectBindingVerificationRun, error) {
	f.writes++
	f.graphRequest = r
	return api.ProjectBindingVerificationRun{}, f.cause
}
func (f *createRecoveryFake) read(ctx context.Context) error {
	f.reads++
	switch f.mode {
	case "absent":
		return &api.Problem{Status: 404}
	case "read_forbidden":
		return &api.Problem{Status: 403}
	case "read_failed":
		return &api.Problem{Status: 503}
	case "retry":
		if f.reads == 1 {
			return &api.Problem{Status: 503}
		}
	case "deadline":
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (f *createRecoveryFake) GetBindingVerificationRun(ctx context.Context, _, _ string) (api.BindingVerificationRun, error) {
	if err := f.read(ctx); err != nil {
		return api.BindingVerificationRun{}, err
	}
	return f.app, nil
}
func (f *createRecoveryFake) GetProjectBindingVerificationRun(ctx context.Context, _, _, _ string) (api.ProjectBindingVerificationRun, error) {
	if err := f.read(ctx); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	return f.graph, nil
}
func TestCreationAutomaticReconciliation(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, mode := range []string{"committed", "gateway", "absent", "read_forbidden", "read_failed", "retry", "drift", "ttl", "consent", "budget", "expired", "cancelled", "failed_resource", "member", "passed_incomplete", "duplicate", "callback", "cancel", "deadline", "forbidden", "mutation"} {
			t.Run(mode+map[bool]string{true: "/graph", false: "/app"}[graph], func(t *testing.T) {
				c, r, g, o := creationFixture(graph)
				f := &createRecoveryFake{creationFake: c, mode: mode, cause: io.ErrUnexpectedEOF}
				// Runnable plans are required for automatic continuation; archive inspection remains permissive.
				normalize := func(m api.BindingVerificationRun, v api.BindingVerificationReview) (api.BindingVerificationRun, api.BindingVerificationReview) {
					for i := range m.Steps {
						if m.Steps[i].Plan.Action == "task" {
							m.Steps[i].Plan.Action = "probe"
						}
						v.Plan[i] = m.Steps[i].Plan
					}
					return m, v
				}
				if graph {
					for n, m := range c.graph.Members {
						v := g.Members[n]
						m, v = normalize(m, v)
						c.graph.Members[n] = m
						g.Members[n] = v
					}
				} else {
					c.app, r = normalize(c.app, r)
				}
				if mode == "gateway" {
					f.cause = &api.Problem{Status: 503}
				}
				if mode == "forbidden" {
					f.cause = &api.Problem{Status: 403}
				}
				mutate := func(m api.BindingVerificationRun) api.BindingVerificationRun {
					switch mode {
					case "drift":
						m.ReviewDigest = "changed"
					case "ttl":
						m.ExpiresAt = m.ExpiresAt.Add(time.Second)
					case "consent":
						m.QueuePublish = !m.QueuePublish
					case "budget":
						m.MaxConcurrency = 8
					case "expired":
						m.CreatedAt = time.Now().Add(-2 * time.Minute)
						m.ExpiresAt = m.CreatedAt.Add(time.Minute)
					case "cancelled":
						m.Status = "cancelled"
					case "failed_resource":
						m.Steps[0].Owned = true
						m.Steps[0].ResourceKind = "task"
						m.Steps[0].ResourceID = "55555555-5555-4555-8555-000000000001"
						m.Steps[0].Status = "failed"
					case "passed_incomplete":
						m.Status = "passed"
					}
					return m
				}
				if graph {
					for n, m := range c.graph.Members {
						c.graph.Members[n] = mutate(m)
					}
					if mode == "expired" {
						for _, m := range c.graph.Members {
							c.graph.CreatedAt = m.CreatedAt
							c.graph.ExpiresAt = m.ExpiresAt
							break
						}
					}
					if mode == "member" {
						delete(c.graph.Members, "billing")
					}
					if mode == "duplicate" {
						m := c.graph.Members["billing"]
						m.ID = c.graph.Members["api"].ID
						c.graph.Members["billing"] = m
					}
				} else {
					c.app = mutate(c.app)
					if mode == "member" {
						c.app.GraphRunID = "unexpected"
					}
					if mode == "duplicate" {
						c.app.ID = "55555555-5555-4555-8555-000000000001"
					}
				}
				ctx, cancel := context.WithCancel(testContext(t))
				defer cancel()
				if mode == "deadline" {
					var end context.CancelFunc
					ctx, end = context.WithTimeout(ctx, 20*time.Millisecond)
					defer end()
				}
				zero := 0
				o.StatusReadRetries = &zero
				if mode == "retry" {
					one := 1
					o.StatusReadRetries = &one
				}
				events := []Progress{}
				cb := errors.New("checkpoint failed")
				o.OnProgress = func(p Progress) error {
					events = append(events, p)
					if p.Status == "reconciling" {
						if mode == "callback" {
							return cb
						}
						if mode == "cancel" {
							cancel()
						}
						if mode == "mutation" {
							if graph {
								v := g.Members["api"]
								v.Plan[0].Name = "changed"
							} else {
								r.Plan[0].Name = "changed"
							}
						}
					}
					return nil
				}
				var err error
				if graph {
					_, err = CreateProject(ctx, f, "project", "production", g, o)
				} else {
					_, err = CreateApp(ctx, f, "api", r, o)
				}
				success := mode == "committed" || mode == "gateway" || mode == "retry" || mode == "mutation"
				if (err == nil) != success {
					t.Fatalf("success=%v err=%v reads=%d", success, err, f.reads)
				}
				if f.writes != 1 {
					t.Fatalf("POST count=%d", f.writes)
				}
				if err != nil {
					var e *CreationError
					if !errors.As(err, &e) || !e.Attempted || e.RunID != o.RunID || !errors.Is(err, f.cause) {
						t.Fatalf("lost cause/identity: %v", err)
					}
				}
				if mode == "callback" && !errors.Is(err, cb) {
					t.Fatal("lost callback cause")
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("lost cancellation")
				}
				if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("lost deadline")
				}
				if mode == "forbidden" {
					if len(events) != 0 || f.reads != 0 {
						t.Fatal("permanent error triggered recovery")
					}
				} else {
					if len(events) == 0 || events[0].Status != "reconciling" || events[0].Operation != "creation" {
						t.Fatal(events)
					}
				}
				if (mode == "callback" || mode == "cancel") && f.reads != 0 {
					t.Fatal("read after local stop")
				}
				if mode == "read_failed" && f.reads != 1 {
					t.Fatal("zero retries ignored")
				}
				if mode == "retry" && (f.reads != 2 || len(events) != 2 || events[1].Status != "retrying") {
					t.Fatal("bounded retry/progress missing")
				}
			})
		}
	}
}

func TestCreationRetryBudgetValidation(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, n := range []int{-1, 9} {
			f, r, g, o := creationFixture(graph)
			o.StatusReadRetries = &n
			var err error
			if graph {
				_, err = CreateProject(testContext(t), f, "project", "production", g, o)
			} else {
				_, err = CreateApp(testContext(t), f, "api", r, o)
			}
			var e *CreationError
			if !errors.As(err, &e) || e.Attempted || f.writes != 0 || f.reads != 0 {
				t.Fatalf("invalid retry budget attempted I/O: %v", err)
			}
		}
	}
}

func TestCreationPreservesPlanRepresentationForCustomClients(t *testing.T) {
	f, r, _, o := creationFixture(false)
	at := time.Now()
	scope := "production"
	for i := range r.Plan {
		r.Plan[i].Operations = []string{}
		r.Plan[i].CheckedAt = &at
		r.Plan[i].QueueEnvironment = &scope
		f.app.Steps[i].Plan = r.Plan[i]
	}
	if _, err := CreateApp(testContext(t), f, "api", r, o); err != nil {
		t.Fatalf("valid custom-client receipt changed during freezing: %v", err)
	}
}

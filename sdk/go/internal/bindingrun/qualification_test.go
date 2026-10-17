package bindingrun

import (
	"context"
	"errors"
	"github.com/poyrazK/faas/sdk/go/internal/api"
	"testing"
	"time"
)

type qualificationClient struct {
	*fakeClient
	completions, reads int
	mode               string
}

func (f *qualificationClient) qualification() *api.BindingVerificationQualification {
	app := "api"
	return &api.BindingVerificationQualification{CheckedAt: time.Now().UTC(), Conditions: []api.BindingVerificationQualificationCondition{{App: app, Code: "runtime_updating", Scope: "production"}}}
}
func (f *qualificationClient) CompleteBindingVerificationRun(ctx context.Context, app, id string) (api.BindingVerificationRun, error) {
	f.completions++
	if f.completions == 1 || f.mode == "deadline" {
		r := *f.root.app
		r.Qualification = f.qualification()
		if f.mode == "malformed" {
			r.Qualification.Conditions[0].App = "foreign"
		}
		if f.mode == "unknown" {
			r.Qualification.Conditions[0].Code = "unrecognized"
		}
		return r, nil
	}
	return f.fakeClient.CompleteBindingVerificationRun(ctx, app, id)
}
func (f *qualificationClient) CompleteProjectBindingVerificationRun(ctx context.Context, p, env, id string) (api.ProjectBindingVerificationRun, error) {
	f.completions++
	if f.completions == 1 || f.mode == "deadline" {
		r := *f.root.graph
		r.Qualification = f.qualification()
		if f.mode == "malformed" {
			r.Qualification.Conditions[0].App = "foreign"
		}
		if f.mode == "unknown" {
			r.Qualification.Conditions[0].Code = "unrecognized"
		}
		return r, nil
	}
	return f.fakeClient.CompleteProjectBindingVerificationRun(ctx, p, env, id)
}
func (f *qualificationClient) GetBindingVerificationRun(ctx context.Context, app, id string) (api.BindingVerificationRun, error) {
	f.reads++
	r, e := f.fakeClient.GetBindingVerificationRun(ctx, app, id)
	if f.mode == "drift" && f.completions > 0 {
		r.ReviewDigest = "changed"
	}
	return r, e
}
func (f *qualificationClient) GetProjectBindingVerificationRun(ctx context.Context, p, env, id string) (api.ProjectBindingVerificationRun, error) {
	f.reads++
	r, e := f.fakeClient.GetProjectBindingVerificationRun(ctx, p, env, id)
	if f.mode == "drift" && f.completions > 0 {
		r.ReviewDigest = "changed"
	}
	return r, e
}
func TestRunnerPendingQualification(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, mode := range []string{"settles", "malformed", "unknown", "drift", "cancel", "callback", "deadline"} {
			t.Run(map[bool]string{true: "graph", false: "app"}[graph]+"/"+mode, func(t *testing.T) {
				f := &qualificationClient{fakeClient: newFake(contractCase{Graph: graph, Actions: []string{"observe"}}), mode: mode}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "deadline" {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, 30*time.Millisecond)
					defer stop()
				}
				observed := false
				callbackErr := errors.New("observer failed")
				o := Options{SkipDiagnostics: true, PollInterval: time.Millisecond, OnProgress: func(p Progress) error {
					if p.Status == "waiting_for_qualification" {
						observed = true
						if p.Qualification == nil {
							t.Fatal("missing condition")
						}
						if mode == "cancel" {
							cancel()
						}
						if mode == "callback" {
							return callbackErr
						}
					}
					return nil
				}}
				var e error
				id := f.root.id()
				if graph {
					_, e = RunProject(ctx, f, "project", "production", id, o)
				} else {
					_, e = RunApp(ctx, f, "api", id, o)
				}
				if mode == "settles" {
					if e != nil || f.completions != 2 || f.admits != 0 || !observed {
						t.Fatalf("settled: %v %+v", e, f)
					}
					return
				}
				var recovery *RecoveryError
				if !errors.As(e, &recovery) || recovery.RunID != id || f.admits != 0 {
					t.Fatalf("failed: %v", e)
				}
				if mode == "cancel" && !errors.Is(e, context.Canceled) || mode == "deadline" && !errors.Is(e, context.DeadlineExceeded) || mode == "callback" && !errors.Is(e, callbackErr) {
					t.Fatalf("wrong cause %v", e)
				}
				if (mode == "malformed" || mode == "unknown" || mode == "callback" || mode == "cancel") && f.completions != 1 {
					t.Fatal("completion retried after hard stop")
				}
			})
		}
	}
}

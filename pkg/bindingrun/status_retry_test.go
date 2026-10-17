package bindingrun

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"testing"
	"time"
)

func TestStatusReadRetryClassifications(t *testing.T) {
	for _, s := range []struct {
		err   error
		retry bool
	}{{io.EOF, true}, {context.Canceled, false}, {context.DeadlineExceeded, true}, {errors.New("invalid receipt"), false}, {&api.APIError{Problem: api.Problem{Status: 503}}, true}, {&api.APIError{Problem: api.Problem{Status: 403}}, false}, {&api.APIError{Problem: api.Problem{Status: 429}}, false}, {&api.APIError{Problem: api.Problem{Status: 500}}, false}} {
		if statusReadRetryable(s.err) != s.retry {
			t.Fatalf("classification: %v", s.err)
		}
	}
	for attempt := 1; attempt <= api.BindingVerificationRunnerMaxStatusReadRetries; attempt++ {
		delay := statusReadRetryDelay(attempt)
		cap := min(250*time.Millisecond<<(attempt-1), 2*time.Second)
		if delay < cap/2 || delay > cap {
			t.Fatalf("delay=%s", delay)
		}
	}
}

type statusRetryClient struct {
	*fakeClient
	failures, reads int
	fault           error
	drift           bool
	resumeFault     bool
}

func (f *statusRetryClient) GetBindingVerificationRun(ctx context.Context, app, id string) (api.BindingVerificationRun, error) {
	f.reads++
	if f.failures > 0 {
		f.failures--
		return api.BindingVerificationRun{}, f.fault
	}
	r, e := f.fakeClient.GetBindingVerificationRun(ctx, app, id)
	if f.drift {
		r.ReviewDigest = "changed"
	}
	return r, e
}
func (f *statusRetryClient) GetProjectBindingVerificationRun(ctx context.Context, p, env, id string) (api.ProjectBindingVerificationRun, error) {
	f.reads++
	if f.failures > 0 {
		f.failures--
		return api.ProjectBindingVerificationRun{}, f.fault
	}
	r, e := f.fakeClient.GetProjectBindingVerificationRun(ctx, p, env, id)
	if f.drift {
		r.ReviewDigest = "changed"
	}
	return r, e
}
func (f *statusRetryClient) ResumeBindingVerificationRun(ctx context.Context, app, id string) (api.BindingVerificationRun, error) {
	if f.resumeFault {
		return api.BindingVerificationRun{}, f.fault
	}
	return f.fakeClient.ResumeBindingVerificationRun(ctx, app, id)
}
func TestStatusReadRetriesPreserveReceipt(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, mode := range []string{"recover", "exhaust", "disable", "forbidden", "drift", "callback", "cancel", "deadline", "resume"} {
			t.Run(map[bool]string{false: "app", true: "graph"}[graph]+"/"+mode, func(t *testing.T) {
				f := &statusRetryClient{fakeClient: newFake(contractCase{Graph: graph, Actions: []string{"probe"}}), failures: 1, fault: io.EOF}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				retries := 1
				events := []Progress{}
				callbackError := errors.New("observer failed")
				o := Options{PollInterval: time.Millisecond, StatusReadRetries: &retries, SkipDiagnostics: true, OnProgress: func(p Progress) error {
					events = append(events, p)
					if p.Status == "retrying" {
						switch mode {
						case "callback":
							return callbackError
						case "cancel":
							cancel()
						}
					}
					return nil
				}}
				switch mode {
				case "exhaust":
					f.failures = 5
				case "disable":
					retries = 0
				case "forbidden":
					f.fault = &api.APIError{Problem: api.Problem{Status: 403}}
				case "drift":
					f.drift = true
				case "deadline":
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, 40*time.Millisecond)
					defer stop()
				case "resume":
					f.resumeFault = true
				}
				id := f.root.id()
				var err error
				if graph {
					if mode == "resume" {
						return
					}
					_, err = RunProject(ctx, f, "project", "production", id, o)
				} else {
					_, err = RunApp(ctx, f, "api", id, o)
				}
				if mode == "recover" {
					if err != nil || f.reads != 2 || f.admits != 1 {
						t.Fatalf("recovery: %v %+v", err, f)
					}
					if events[len(events)-1].Status != "passed" {
						t.Fatal("missing final progress")
					}
					return
				}
				var recovery *RecoveryError
				if !errors.As(err, &recovery) || recovery.RunID != id || f.admits > 1 {
					t.Fatalf("recovery identity: %v", err)
				}
				switch mode {
				case "exhaust":
					if !errors.Is(err, io.EOF) || f.reads != 2 {
						t.Fatal(err)
					}
				case "disable", "forbidden":
					if f.reads != 1 || !errors.Is(err, f.fault) {
						t.Fatal(err)
					}
				case "callback":
					if !errors.Is(err, callbackError) || f.reads != 1 {
						t.Fatal(err)
					}
				case "cancel":
					if !errors.Is(err, context.Canceled) || f.reads != 1 {
						t.Fatal(err)
					}
				case "deadline":
					if !errors.Is(err, context.DeadlineExceeded) || f.reads < 1 || f.reads > 2 {
						t.Fatal(err)
					}
				case "resume":
					if f.reads != 0 || !errors.Is(err, io.EOF) {
						t.Fatal(err)
					}
				case "drift":
					if f.reads != 2 {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

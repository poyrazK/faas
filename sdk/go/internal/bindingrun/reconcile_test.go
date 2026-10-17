package bindingrun

import (
	"context"
	"errors"
	"github.com/poyrazK/faas/sdk/go/internal/api"
	"io"
	"testing"
	"time"
)

type reconciliationClient struct {
	*fakeClient
	phase, mode   string
	uncertain     bool
	writes, reads int
	cause         error
}

func (c *reconciliationClient) AdmitBindingVerificationRunStep(ctx context.Context, app, id string, index int) (api.BindingVerificationRunStep, error) {
	if c.phase != "admission" || c.uncertain {
		return c.fakeClient.AdmitBindingVerificationRunStep(ctx, app, id, index)
	}
	c.writes++
	c.uncertain = true
	if c.mode != "absent" && c.mode != "forbidden" {
		if _, err := c.fakeClient.AdmitBindingVerificationRunStep(ctx, app, id, index); err != nil {
			return api.BindingVerificationRunStep{}, err
		}
	}
	return api.BindingVerificationRunStep{}, c.cause
}
func (c *reconciliationClient) complete() (snapshot, error) {
	if c.phase != "completion" || c.uncertain {
		return c.fakeClient.Complete()
	}
	c.writes++
	c.uncertain = true
	if c.mode != "absent" && c.mode != "forbidden" {
		if _, err := c.fakeClient.Complete(); err != nil {
			return snapshot{}, err
		}
	}
	return snapshot{}, c.cause
}
func (c *reconciliationClient) CompleteBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error) {
	s, e := c.complete()
	if e != nil {
		return api.BindingVerificationRun{}, e
	}
	return *s.app, nil
}
func (c *reconciliationClient) CompleteProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error) {
	s, e := c.complete()
	if e != nil {
		return api.ProjectBindingVerificationRun{}, e
	}
	return *s.graph, nil
}
func (c *reconciliationClient) read(ctx context.Context) (snapshot, error) {
	if c.uncertain && c.mode == "expires" {
		<-ctx.Done()
		return snapshot{}, ctx.Err()
	}
	if c.uncertain {
		c.reads++
		if c.mode == "read_failed" || c.mode == "retry" && c.reads == 1 {
			return snapshot{}, &api.Problem{Status: 503}
		}
	}
	s := c.fakeClient.Read(true)
	if c.uncertain {
		switch c.mode {
		case "drift":
			if s.app != nil {
				s.app.ReviewDigest = "changed"
			} else {
				s.graph.ReviewDigest = "changed"
			}
		case "deadline":
			if s.app != nil {
				s.app.ExpiresAt = s.app.ExpiresAt.Add(time.Second)
			} else {
				s.graph.ExpiresAt = s.graph.ExpiresAt.Add(time.Second)
			}
		case "unowned":
			for _, m := range s.members() {
				for i, step := range m.Steps {
					step.Owned = false
					s.setStep(m.App, i, step)
				}
			}
		case "malformed":
			for _, m := range s.members() {
				for i, step := range m.Steps {
					step.ResourceID = "bad"
					s.setStep(m.App, i, step)
				}
			}
		case "member_pending":
			if s.graph != nil {
				m := s.graph.Members["billing"]
				m.Status = "pending"
				s.graph.Members["billing"] = m
			} else {
				s.app.Status = "pending"
			}
		}
	}
	return s, nil
}
func (c *reconciliationClient) GetBindingVerificationRun(ctx context.Context, _ string, _ string) (api.BindingVerificationRun, error) {
	s, e := c.read(ctx)
	if e != nil {
		return api.BindingVerificationRun{}, e
	}
	return *s.app, nil
}
func (c *reconciliationClient) GetProjectBindingVerificationRun(ctx context.Context, _ string, _ string, _ string) (api.ProjectBindingVerificationRun, error) {
	s, e := c.read(ctx)
	if e != nil {
		return api.ProjectBindingVerificationRun{}, e
	}
	return *s.graph, nil
}
func TestRunnerReconcilesUncertainWrites(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, phase := range []string{"admission", "completion"} {
			for _, mode := range []string{"committed", "gateway", "absent", "drift", "deadline", "unowned", "malformed", "read_failed", "retry", "forbidden", "callback", "cancel", "expires", "member_pending"} {
				t.Run(phase+"/"+mode+map[bool]string{true: "/graph", false: "/app"}[graph], func(t *testing.T) {
					if mode == "member_pending" && phase == "admission" {
						t.Skip("completion-only member confirmation")
					}
					f := newFake(contractCase{Graph: graph, Actions: []string{"probe", "queue-canary"}})
					c := &reconciliationClient{fakeClient: f, phase: phase, mode: mode, cause: io.ErrUnexpectedEOF}
					if mode == "gateway" {
						c.cause = &api.Problem{Status: 503}
					}
					if mode == "forbidden" {
						c.cause = &api.Problem{Status: 403}
					}
					ctx, cancel := context.WithCancel(testContext(t))
					if mode == "expires" {
						var deadlineCancel context.CancelFunc
						ctx, deadlineCancel = context.WithTimeout(ctx, 20*time.Millisecond)
						defer deadlineCancel()
					}
					defer cancel()
					events := []Progress{}
					callbackErr := errors.New("checkpoint failed")
					retries := 0
					if mode == "retry" {
						retries = 1
					}
					options := Options{SkipDiagnostics: true, StatusReadRetries: &retries, PollInterval: time.Millisecond, OnProgress: func(p Progress) error {
						events = append(events, p)
						if p.Status == "reconciling" {
							if mode == "callback" {
								return callbackErr
							}
							if mode == "cancel" {
								cancel()
							}
						}
						return nil
					}}
					var err error
					if graph {
						_, err = RunProject(ctx, c, "project", "production", f.root.id(), options)
					} else {
						_, err = RunApp(ctx, c, "api", f.root.id(), options)
					}
					success := mode == "committed" || mode == "gateway" || mode == "retry"
					if (err == nil) != success {
						t.Fatalf("success=%v err=%v calls=%v", success, err, f.calls)
					}
					if err != nil {
						var recovery *RecoveryError
						if !errors.As(err, &recovery) || recovery.RunID != f.root.id() || !errors.Is(err, c.cause) {
							t.Fatalf("lost original cause/identity: %v", err)
						}
					}
					if c.writes != 1 {
						t.Fatalf("uncertain writes=%d", c.writes)
					}
					found := false
					for _, p := range events {
						if p.Status == "reconciling" {
							found = true
							if p.Operation != phase {
								t.Fatal(p)
							}
						}
					}
					if found != (mode != "forbidden") {
						t.Fatalf("reconciling=%v", found)
					}
					if (mode == "callback" || mode == "cancel" || mode == "forbidden") && c.reads != 0 {
						t.Fatal("read after local stop or permanent error")
					}
					if mode == "callback" && !errors.Is(err, callbackErr) {
						t.Fatal("lost callback cause")
					}
					if mode == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("lost cancellation")
					}
					if mode == "expires" && !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("lost caller deadline")
					}
					if success && phase == "completion" {
						count := 0
						for _, call := range f.calls {
							if call == "complete" {
								count++
							}
						}
						if count != 1 {
							t.Fatalf("completion writes=%d", count)
						}
					}
					if success && f.admits != 2 {
						t.Fatalf("admissions=%d", f.admits)
					}
				})
			}
		}
	}
}

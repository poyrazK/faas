package functions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WithIdempotencyKey makes a retried Start return the same run.
func WithIdempotencyKey(key string) Option { return WithHeader("Idempotency-Key", key) }

// RunStatus is what a function reads about a workflow run it started.
type RunStatus struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Done   bool            `json:"done"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Run is a workflow run this function started; only it reads or signals the
// run (ADR-20261113120824 §3).
type Run struct {
	ID          string
	Function    string
	platformURL string
	client      *http.Client
}

// Start starts a run of the named workflow function (workflow: true) and
// returns its handle at once.
func Start[In any](ctx context.Context, name string, in In, opts ...Option) (*Run, error) {
	o := resolve(opts)
	if _, err := o.target(name); err != nil {
		return nil, err
	}
	platform := o.env("GREGALE_PLATFORM_API_URL")
	if platform == "" {
		platform = PlatformAPIURL
	}
	platform = strings.TrimRight(platform, "/")
	input, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("function %s: encode input: %w", name, err)
	}
	body, _ := json.Marshal(map[string]json.RawMessage{"function": mustJSON(name), "input": input})
	raw, status, err := platformDo(ctx, o.client, name, http.MethodPost, platform+"/v1/workflow-runs", body, o.headers)
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated {
		return nil, problemError(name, status, raw)
	}
	var st RunStatus
	if json.Unmarshal(raw, &st) != nil || st.ID == "" {
		return nil, &Error{Function: name, Status: status, Detail: "workflow run id missing"}
	}
	return &Run{ID: st.ID, Function: name, platformURL: platform, client: o.client}, nil
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func platformDo(ctx context.Context, client *http.Client, name, method, u string, body []byte, headers http.Header) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, 0, err
	}
	for key, values := range headers {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("function %s: %w", name, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	return raw, res.StatusCode, err
}

func (r *Run) read(ctx context.Context, waitSeconds int) (RunStatus, error) {
	u := r.platformURL + "/v1/workflow-runs/" + url.PathEscape(r.ID)
	if waitSeconds > 0 {
		u += fmt.Sprintf("?wait=%d", waitSeconds)
	}
	raw, status, err := platformDo(ctx, r.client, r.Function, http.MethodGet, u, nil, nil)
	if err != nil {
		return RunStatus{}, err
	}
	if status != http.StatusOK {
		return RunStatus{}, problemError(r.Function, status, raw)
	}
	var st RunStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return RunStatus{}, fmt.Errorf("workflow run %s: decode status: %w", r.ID, err)
	}
	return st, nil
}

// Status returns the run's current state.
func (r *Run) Status(ctx context.Context) (RunStatus, error) { return r.read(ctx, 0) }

// Wait long-polls until the run finishes and decodes its output into out
// (which may be nil). A failed run returns an *Error with code
// workflow_run_failed; a context that ends first returns ErrPending wrapped
// with the context's error.
func (r *Run) Wait(ctx context.Context, out any) error {
	for {
		wait := maxWaitSeconds
		if deadline, ok := ctx.Deadline(); ok {
			wait = min(wait, max(0, int(time.Until(deadline).Seconds())))
		}
		st, err := r.read(ctx, wait)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w: %w", ErrPending, ctx.Err())
			}
			return err
		}
		if st.Done {
			if st.Status != "succeeded" {
				detail := st.Error
				if detail == "" {
					detail = st.Status
				}
				return &Error{Function: r.Function, Status: http.StatusInternalServerError, Code: "workflow_run_failed", Detail: detail}
			}
			if out == nil || len(st.Output) == 0 {
				return nil
			}
			return json.Unmarshal(st.Output, out)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrPending, err)
		}
	}
}

// Signal sends the run an event its WorkflowWaitForEvent may take.
func (r *Run) Signal(ctx context.Context, event string, payload any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{"event": event, "payload": payload})
	if err != nil {
		return fmt.Errorf("workflow run %s: encode payload: %w", r.ID, err)
	}
	raw, status, err := platformDo(ctx, r.client, r.Function, http.MethodPost, r.platformURL+"/v1/workflow-runs/"+url.PathEscape(r.ID)+"/events", body, nil)
	if err != nil {
		return err
	}
	if status != http.StatusAccepted {
		return problemError(r.Function, status, raw)
	}
	return nil
}

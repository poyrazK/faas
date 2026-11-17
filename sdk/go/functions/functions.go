// Package functions calls the other functions of a Gregale functions project
// from a Go function.
//
//	user, err := functions.Call[UserInput, User](ctx, "users", UserInput{Email: "a@example.com"})
//	sent, err := functions.Send(ctx, "mailer", Mail{To: user.Email})
//	var receipt Receipt
//	err = sent.Wait(ctx, &receipt)
//
// A deploy binds each function to its siblings (and to the apps under its
// calls:) and sets GREGALE_FUNCTIONS, a JSON map from each callable name to
// its app slug. Calls go over the private binding, never the public internet;
// the platform authorizes them by the calling VM and wakes a parked callee,
// and the request's deadline and release travel with them. Send enqueues a
// durable async invocation; only the function that sent it can read it back.
// `gregale function types` writes gregale_functions.go with typed wrappers
// generated from each function's input and output schema.
package functions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	faas "github.com/gregale-dev/gregale-go"
)

const (
	// FunctionsEnv names the callable functions and their app slugs.
	FunctionsEnv = "GREGALE_FUNCTIONS"
	// PlatformAPIURL is where a function reads the invocations it sent.
	PlatformAPIURL = "http://api.svc.gregale:10081"

	functionCallHeader = "X-Gregale-Function-Call"
	invokeHeader       = "X-Gregale-Invoke"
	onCompleteHeader   = "X-Gregale-On-Complete"
	maxWaitSeconds     = 30
)

// Error is a non-2xx answer from a callee or the platform. Code is the
// problem's stable code, such as function_input_invalid.
type Error struct {
	Function string
	Status   int
	Code     string
	Detail   string
	Problem  map[string]any
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("function %s answered %d", e.Function, e.Status)
	if e.Code != "" {
		msg += " (" + e.Code + ")"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// Option changes one call or send.
type Option func(*options)

type options struct {
	path       string
	headers    http.Header
	onComplete string
	client     *http.Client
	env        func(string) string
}

// WithPath sets the request path on the callee; the default is "/".
func WithPath(path string) Option { return func(o *options) { o.path = path } }

// WithHeader adds a request header.
func WithHeader(name, value string) Option {
	return func(o *options) { o.headers.Add(name, value) }
}

// WithOnComplete names the function that receives a send's completion when it
// finishes: a name this function may call, or "self".
func WithOnComplete(name string) Option { return func(o *options) { o.onComplete = name } }

// WithHTTPClient replaces the client; its transport should keep the Gregale
// release transport so the deadline and release travel with the call.
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithEnv replaces os.Getenv, for tests.
func WithEnv(getenv func(string) string) Option { return func(o *options) { o.env = getenv } }

var defaultClient = &http.Client{Transport: faas.NewGregaleReleaseTransport(http.DefaultTransport), Timeout: 0}

func resolve(opts []Option) options {
	o := options{path: "/", headers: http.Header{}, client: defaultClient, env: os.Getenv}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Callables parses GREGALE_FUNCTIONS; a missing or malformed value is empty.
func Callables(getenv func(string) string) map[string]string {
	if getenv == nil {
		getenv = os.Getenv
	}
	out := map[string]string{}
	var parsed map[string]any
	if json.Unmarshal([]byte(getenv(FunctionsEnv)), &parsed) != nil {
		return out
	}
	for name, slug := range parsed {
		if s, ok := slug.(string); ok && s != "" {
			out[name] = s
		}
	}
	return out
}

var serviceEnvUnsafe = regexp.MustCompile(`[^A-Z0-9]`)

// BaseURL is the binding URL for slug: its injected GREGALE_SERVICE_<SLUG>_URL,
// else the private alias.
func BaseURL(slug string, getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if u := getenv("GREGALE_SERVICE_" + serviceEnvUnsafe.ReplaceAllString(strings.ToUpper(slug), "_") + "_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://" + slug + ".internal:10081"
}

func (o options) target(name string) (string, error) {
	callables := Callables(o.env)
	slug, ok := callables[name]
	if !ok {
		known := make([]string, 0, len(callables))
		for n := range callables {
			known = append(known, n)
		}
		sort.Strings(known)
		hint := FunctionsEnv + " is not set"
		if len(known) > 0 {
			hint = "callable: " + strings.Join(known, ", ")
		}
		return "", fmt.Errorf("function %s is not callable from here; %s (deploy the functions project, or add the app to calls:)", name, hint)
	}
	path := o.path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return BaseURL(slug, o.env) + path, nil
}

func post(ctx context.Context, name string, in any, send bool, o options) (*http.Response, []byte, error) {
	target, err := o.target(name)
	if err != nil {
		return nil, nil, err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, nil, fmt.Errorf("function %s: encode input: %w", name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for k, vs := range o.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(functionCallHeader, "1")
	if send {
		req.Header.Set(invokeHeader, "async")
		if o.onComplete != "" {
			req.Header.Set(onCompleteHeader, o.onComplete)
		}
	}
	res, err := o.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("function %s: %w", name, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("function %s: read answer: %w", name, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, nil, problemError(name, res.StatusCode, raw)
	}
	return res, raw, nil
}

func problemError(name string, status int, raw []byte) *Error {
	e := &Error{Function: name, Status: status}
	if json.Unmarshal(raw, &e.Problem) == nil {
		e.Code, _ = e.Problem["code"].(string)
		e.Detail, _ = e.Problem["detail"].(string)
	} else if len(raw) > 200 {
		e.Detail = string(raw[:200])
	} else {
		e.Detail = string(raw)
	}
	return e
}

// Call POSTs in to the named function as JSON and decodes its JSON answer.
func Call[In, Out any](ctx context.Context, name string, in In, opts ...Option) (Out, error) {
	var out Out
	_, raw, err := post(ctx, name, in, false, resolve(opts))
	if err != nil {
		return out, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("function %s: decode output: %w", name, err)
	}
	return out, nil
}

// Send enqueues a durable async invocation of the named function and returns
// at once.
func Send[In any](ctx context.Context, name string, in In, opts ...Option) (*Invocation, error) {
	o := resolve(opts)
	_, raw, err := post(ctx, name, in, true, o)
	if err != nil {
		return nil, err
	}
	var accepted struct {
		ID           string `json:"id"`
		InvocationID string `json:"invocation_id"`
	}
	_ = json.Unmarshal(raw, &accepted)
	id := accepted.InvocationID
	if id == "" {
		id = accepted.ID
	}
	if id == "" {
		return nil, &Error{Function: name, Status: http.StatusAccepted, Detail: "async invocation id missing"}
	}
	platform := o.env("GREGALE_PLATFORM_API_URL")
	if platform == "" {
		platform = PlatformAPIURL
	}
	return &Invocation{ID: id, Function: name, platformURL: strings.TrimRight(platform, "/"), client: o.client}, nil
}

// Status is what a function reads about an invocation it sent.
type Status struct {
	ID          string          `json:"id"`
	State       string          `json:"state"`
	Done        bool            `json:"done"`
	Outcome     string          `json:"outcome,omitempty"`
	Attempts    int             `json:"attempts"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

// Invocation is an accepted async invocation.
type Invocation struct {
	ID          string
	Function    string
	platformURL string
	client      *http.Client
}

func (i *Invocation) read(ctx context.Context, waitSeconds int) (Status, error) {
	u := i.platformURL + "/v1/invocations/" + url.PathEscape(i.ID)
	if waitSeconds > 0 {
		u += fmt.Sprintf("?wait=%d", waitSeconds)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Status{}, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := i.client.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("invocation %s: %w", i.ID, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return Status{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Status{}, problemError(i.Function, res.StatusCode, raw)
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return Status{}, fmt.Errorf("invocation %s: decode status: %w", i.ID, err)
	}
	return st, nil
}

// Status returns the invocation's current state.
func (i *Invocation) Status(ctx context.Context) (Status, error) { return i.read(ctx, 0) }

// ErrPending is returned by Wait when ctx ends before the invocation does.
var ErrPending = errors.New("functions: invocation still running")

// Wait long-polls until the invocation finishes and decodes its result into
// out (which may be nil). A failed invocation returns an *Error with code
// function_invocation_failed; a context that ends first returns ErrPending
// wrapped with the context's error.
func (i *Invocation) Wait(ctx context.Context, out any) error {
	for {
		wait := maxWaitSeconds
		if deadline, ok := ctx.Deadline(); ok {
			wait = min(wait, max(0, int(time.Until(deadline).Seconds())))
		}
		st, err := i.read(ctx, wait)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w: %w", ErrPending, ctx.Err())
			}
			return err
		}
		if st.Done {
			if st.State != "completed" {
				detail := st.Error
				if detail == "" {
					detail = st.State
				}
				return &Error{Function: i.Function, Status: http.StatusInternalServerError, Code: "function_invocation_failed", Detail: detail}
			}
			if out == nil || len(st.Result) == 0 {
				return nil
			}
			return json.Unmarshal(st.Result, out)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrPending, err)
		}
	}
}

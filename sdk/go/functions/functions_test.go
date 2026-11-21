package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func envFor(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// The wire contract matches the Node and Python clients (ADR-20261111150000,
// ADR-20261112160000): calls POST JSON to the binding marked for the callee's
// input check; sends add the async marker and an optional completion target;
// Wait long-polls the platform API.
func TestCallSendAndWait(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		seen = append(seen, r)
		mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/invocations/"):
			polls++
			if polls == 1 {
				_ = json.NewEncoder(w).Encode(Status{ID: "inv-1", State: "dispatching"})
				return
			}
			_ = json.NewEncoder(w).Encode(Status{ID: "inv-1", State: "completed", Done: true, Result: json.RawMessage(`{"id":"7","name":"Ada"}`)})
		case r.Header.Get("X-Gregale-Invoke") == "async":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"inv-1"}`))
		case strings.Contains(string(body), `"bad"`):
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"function_input_invalid","detail":"input.email is required"}`))
		default:
			_, _ = w.Write([]byte(`{"id":"7","name":"Ada"}`))
		}
	}))
	defer srv.Close()
	env := envFor(map[string]string{
		FunctionsEnv:                     `{"users":"shop-users"}`,
		"GREGALE_SERVICE_SHOP_USERS_URL": srv.URL + "/",
		"GREGALE_PLATFORM_API_URL":       srv.URL,
	})
	opts := []Option{WithEnv(env), WithHTTPClient(srv.Client())}
	ctx := context.Background()

	got, err := Call[map[string]string, user](ctx, "users", map[string]string{"email": "a@b"}, opts...)
	if err != nil || got.Name != "Ada" {
		t.Fatalf("Call = %+v, %v", got, err)
	}
	if seen[0].Method != http.MethodPost || seen[0].Header.Get("X-Gregale-Function-Call") != "1" {
		t.Fatalf("call request = %s %v", seen[0].Method, seen[0].Header)
	}

	var problem *Error
	if _, err := Call[map[string]string, user](ctx, "users", map[string]string{"bad": "1"}, opts...); !errors.As(err, &problem) ||
		problem.Status != 422 || problem.Code != "function_input_invalid" {
		t.Fatalf("bad input error = %v", err)
	}

	sent, err := Send(ctx, "users", map[string]string{"email": "a@b"}, append(opts, WithOnComplete("self"))...)
	if err != nil || sent.ID != "inv-1" {
		t.Fatalf("Send = %+v, %v", sent, err)
	}
	if h := seen[2].Header; h.Get("X-Gregale-On-Complete") != "self" || h.Get("X-Gregale-Invoke") != "async" {
		t.Fatalf("send headers = %v", h)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result user
	if err := sent.Wait(waitCtx, &result); err != nil || result.Name != "Ada" {
		t.Fatalf("Wait = %+v, %v", result, err)
	}
	if q := seen[3].URL.Query().Get("wait"); q == "" {
		t.Fatalf("Wait did not long-poll: %s", seen[3].URL)
	}
}

func TestUncallableNameSaysWhatIsCallable(t *testing.T) {
	_, err := Call[any, any](context.Background(), "nobody", nil, WithEnv(envFor(map[string]string{FunctionsEnv: `{"users":"shop-users"}`})))
	if err == nil || !strings.Contains(err.Error(), "callable: users") {
		t.Fatalf("err = %v", err)
	}
}

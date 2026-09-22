package webhookout_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/webhookout"
)

type dispatchRoundTripFunc func(*http.Request) (*http.Response, error)

func (f dispatchRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// ADR-045: the delivery deadline bounds the retry ladder, not just each POST.
func TestWebhook_Dispatch_CancellationStopsRetries(t *testing.T) {
	for _, when := range []string{"before dispatch", "during request", "during injected backoff"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var requests, sleeps int
			client := &http.Client{Transport: dispatchRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				if when == "during request" {
					cancel()
					return nil, r.Context().Err()
				}
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader("retry later")),
					Header:     make(http.Header),
				}, nil
			})}
			d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
				HTTPClient:  client,
				MaxAttempts: 3,
				Sleeper: func(time.Duration) {
					sleeps++
					cancel()
				},
			})
			if when == "before dispatch" {
				cancel()
			}
			res := d.Dispatch(ctx, testTarget("https://receiver.example/webhook"), newTestEvent())
			if !errors.Is(res.Err, context.Canceled) || errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
				t.Errorf("error = %v, want cancellation without exhausted retries", res.Err)
			}
			wantAttempts, wantSleeps := 1, 0
			if when == "before dispatch" {
				wantAttempts = 0
			}
			if when == "during injected backoff" {
				wantSleeps = 1
			}
			if res.Attempts != wantAttempts || requests != wantAttempts || sleeps != wantSleeps {
				t.Errorf("attempts/requests/sleeps = %d/%d/%d, want %d/%d/%d",
					res.Attempts, requests, sleeps, wantAttempts, wantAttempts, wantSleeps)
			}
		})
	}
}

// ADR-045: cancellation must interrupt the real backoff, not wait for it to end.
func TestWebhook_Dispatch_DeadlineInterruptsBackoff(t *testing.T) {
	client := &http.Client{Transport: dispatchRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("retry later")),
			Header:     make(http.Header),
		}, nil
	})}
	d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		HTTPClient:  client,
		MaxAttempts: 2,
		BaseBackoff: 2 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	res := d.Dispatch(ctx, testTarget("https://receiver.example/webhook"), newTestEvent())
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("canceled dispatch took %v, want < 1s", elapsed)
	}
	if !errors.Is(res.Err, context.DeadlineExceeded) || errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
		t.Errorf("error = %v, want deadline without exhausted retries", res.Err)
	}
	if res.Attempts != 1 || res.StatusCode != http.StatusServiceUnavailable || string(res.BodyPrefix) != "retry later" {
		t.Errorf("result = %+v, want the one completed attempt preserved", res)
	}
}

// ADR-045: a broken response body is a retryable transport failure, while
// non-retryable 4xx statuses remain terminal even if their diagnostic body breaks.
func TestWebhook_Dispatch_TruncatedResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "64")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "partial")
			}))
			defer srv.Close()
			d := webhookout.NewDispatcher(webhookout.DispatcherOptions{HTTPClient: srv.Client(), MaxAttempts: 1})
			res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
			if status == http.StatusBadRequest {
				if !errors.Is(res.Err, webhookout.ErrTerminal) {
					t.Errorf("error = %v, want terminal 4xx", res.Err)
				}
			} else if !errors.Is(res.Err, io.ErrUnexpectedEOF) || !errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
				t.Errorf("error = %v, want exhausted retries wrapping unexpected EOF", res.Err)
			}
			if res.Attempts != 1 || res.StatusCode != status || string(res.BodyPrefix) != "partial" {
				t.Errorf("result = %+v, want status and partial diagnostic body preserved", res)
			}
		})
	}
}

func TestWebhook_Dispatch_RetriesTruncatedSuccess(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Length", "64")
			_, _ = io.WriteString(w, "partial")
			return
		}
		_, _ = io.WriteString(w, "complete")
	}))
	defer srv.Close()
	sleeper := &recordingSleeper{}
	d := newTestDispatcher(t, sleeper, srv.Client())
	res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
	if res.Err != nil || res.Attempts != 2 || attempts.Load() != 2 || string(res.BodyPrefix) != "complete" {
		t.Errorf("result = %+v, requests = %d, want successful second attempt", res, attempts.Load())
	}
	if len(sleeper.Delays()) != 1 {
		t.Errorf("backoff count = %d, want 1", len(sleeper.Delays()))
	}
}

// ADR-045: an individual HTTP timeout is retryable, but cancellation of
// the delivery context during the same body-read phase stops the ladder.
func TestWebhook_Dispatch_BodyTimeoutVersusDeliveryCancellation(t *testing.T) {
	for _, cancelDelivery := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_delivery=%t", cancelDelivery), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					w.Header().Set("Content-Length", "64")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					if cancelDelivery {
						cancel()
					}
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, "complete")
			}))
			defer srv.Close()
			client := srv.Client()
			client.Timeout = 100 * time.Millisecond
			d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
				HTTPClient:  client,
				MaxAttempts: 2,
				BaseBackoff: time.Millisecond,
			})
			res := d.Dispatch(ctx, testTarget(srv.URL), newTestEvent())
			if cancelDelivery {
				if !errors.Is(res.Err, context.Canceled) || res.Attempts != 1 || attempts.Load() != 1 {
					t.Errorf("result = %+v, requests = %d, want one canceled attempt", res, attempts.Load())
				}
			} else if res.Err != nil || res.Attempts != 2 || string(res.BodyPrefix) != "complete" || ctx.Err() != nil {
				t.Errorf("result = %+v, want successful retry after per-attempt timeout", res)
			}
		})
	}
}

// ADR-045 / CLAUDE.md §11: receiver-controlled bodies may reflect secrets;
// truncation and CR/LF stripping do not make them safe for logs or error strings.
func TestWebhook_Dispatch_ReflectedBodyStaysOutOfLogsAndErrors(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, testSecret)
			}))
			defer srv.Close()
			var logs safeBuffer
			sleeper := &recordingSleeper{}
			d := webhookout.NewDispatcher(webhookout.DispatcherOptions{
				HTTPClient:  srv.Client(),
				MaxAttempts: 2,
				Sleeper:     sleeper.Sleep,
				Logger:      slog.New(slog.NewJSONHandler(&logs, nil)),
			})
			res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent())
			if !errors.Is(res.Err, webhookout.ErrAttemptsExhausted) {
				t.Fatalf("error = %v, want exhausted retries", res.Err)
			}
			if strings.Contains(logs.String(), testSecret) || strings.Contains(res.Err.Error(), testSecret) {
				t.Error("receiver-controlled secret leaked into logs or the returned error")
			}
			if logs.Len() == 0 || res.StatusCode != status || string(res.BodyPrefix) != testSecret {
				t.Error("safe metadata logs and explicit bounded diagnostics must remain available")
			}
		})
	}
}

// ADR-123 PR-C: only the synthetic delivery gets the test discriminator.
// Reusing the caller's event for a normal delivery must not suppress its alerts.
func TestWebhook_DispatchTest_DoesNotMutateEvent(t *testing.T) {
	for _, format := range []webhookout.DeliveryFormat{webhookout.DeliveryFormatJSON, webhookout.DeliveryFormatCloudEvents} {
		t.Run(string(format), func(t *testing.T) {
			for _, payload := range []map[string]any{nil, {"value": 1.5}, {"value": 1.5, "test": false}, {"test": true}} {
				t.Run(fmt.Sprint(payload), func(t *testing.T) {
					received := make(chan map[string]any, 1)
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Errorf("decode event: %v", err)
						}
						field := "payload"
						if format == webhookout.DeliveryFormatCloudEvents {
							field = "data"
						}
						var got map[string]any
						if err := json.Unmarshal(body[field], &got); err != nil {
							t.Errorf("decode payload: %v", err)
						}
						received <- got
						w.WriteHeader(http.StatusNoContent)
					}))
					defer srv.Close()
					d := webhookout.NewDispatcher(webhookout.DispatcherOptions{HTTPClient: srv.Client(), Format: format})
					evt := newTestEvent()
					evt.Payload = payload
					original := maps.Clone(payload)
					res := d.DispatchTest(context.Background(), testTarget(srv.URL), evt)
					if res.Err != nil {
						t.Fatalf("DispatchTest: %v", res.Err)
					}
					got := <-received
					if got["test"] != true || (payload != nil && got["value"] != original["value"]) {
						t.Errorf("test payload = %v, want test=true with original data", got)
					}
					if !maps.Equal(evt.Payload, original) {
						t.Errorf("caller payload changed to %v, want %v", evt.Payload, original)
					}
					res = d.Dispatch(context.Background(), testTarget(srv.URL), evt)
					if res.Err != nil {
						t.Fatalf("Dispatch: %v", res.Err)
					}
					if got = <-received; !maps.Equal(got, original) {
						t.Errorf("normal delivery payload = %v, want %v", got, original)
					}
				})
			}
		})
	}
}

package eventdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Config struct {
	APIURL, APIToken, ControlToken                 string
	HealthyApp, FailingApp, HealthyURL, FailingURL string
	Source, EventID, EventType, RoutingMode        string
	ExpectedAttempts                               int
}

// Hooks let process tests inject actual crashes. A staging caller may pause
// for an operator restart; the report distinguishes that confirmation from
// the automated process test's evidence.
type Hooks struct {
	Accepted     func(context.Context) error
	RetryPending func(context.Context) error
	DeadLetter   func(context.Context) error
}

type Report struct {
	Gate           string                          `json:"gate"`
	Receipt        api.EventReceiptResponse        `json:"receipt"`
	Healthy        ConsumerStats                   `json:"healthy_consumer"`
	Recovered      ConsumerStats                   `json:"recovered_consumer"`
	HealthyHistory []api.InvocationAttemptResponse `json:"healthy_history"`
	FailureHistory []api.InvocationAttemptResponse `json:"failure_history"`
}

type gate struct {
	cfg                                      Config
	client                                   *http.Client
	healthySubscription, failingSubscription string
	receiptURL                               string
	acceptedAt                               time.Time
}

// Run exercises only public APIs and the dedicated acceptance applications.
// It never writes invocation state or enables routing adoption itself.
func Run(ctx context.Context, cfg Config, hooks Hooks) (Report, error) {
	if err := validateConfig(cfg); err != nil {
		return Report{}, err
	}
	g := &gate{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	return g.run(ctx, hooks)
}

func validateConfig(c Config) error {
	for _, raw := range []string{c.APIURL, c.HealthyURL, c.FailingURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("gate requires absolute HTTP(S) base URLs without credentials, queries or fragments")
		}
	}
	if c.APIToken == "" || c.ControlToken == "" || c.HealthyApp == "" || c.FailingApp == "" || c.Source == "" || c.EventID == "" || c.EventType == "" {
		return fmt.Errorf("gate requires tokens, both apps and a fresh event identity")
	}
	if c.HealthyApp == c.FailingApp || strings.TrimRight(c.HealthyURL, "/") == strings.TrimRight(c.FailingURL, "/") {
		return fmt.Errorf("gate requires two distinct consumer apps and fixture URLs")
	}
	if c.ExpectedAttempts < 2 || c.ExpectedAttempts > api.DurableRetryMaxAttempts || (c.RoutingMode != "event" && c.RoutingMode != "recipient") {
		return fmt.Errorf("gate requires a finite retry budget of at least two and event or recipient routing mode")
	}
	return nil
}

func (g *gate) run(ctx context.Context, hooks Hooks) (Report, error) {
	if err := g.prepare(ctx); err != nil {
		return Report{}, err
	}
	if err := g.publish(ctx); err != nil {
		return Report{}, err
	}
	if hooks.Accepted != nil {
		if err := hooks.Accepted(ctx); err != nil {
			return Report{}, err
		}
	}
	initial, err := g.wait(ctx, func(h, f api.EventReceiptRecipientResponse) bool {
		return h.Execution != nil && h.Execution.State == "completed" && f.Execution != nil &&
			f.Execution.State == "pending" && f.Execution.Attempts == 1 && f.Execution.NextAttemptAt != nil
	})
	if err != nil {
		return Report{}, fmt.Errorf("wait for successful sibling and first handler retry: %w", err)
	}
	healthy, failed, err := g.recipients(initial)
	if err != nil {
		return Report{}, err
	}
	baseline := *healthy.Execution
	if hooks.RetryPending != nil {
		if err := hooks.RetryPending(ctx); err != nil {
			return Report{}, err
		}
	}
	dead, err := g.wait(ctx, func(h, f api.EventReceiptRecipientResponse) bool {
		return f.Execution != nil && f.Execution.State == "dead_letter"
	})
	if err != nil {
		return Report{}, fmt.Errorf("wait for normal retry exhaustion: %w", err)
	}
	_, deadRecipient, err := g.recipients(dead)
	if err != nil {
		return Report{}, err
	}
	if deadRecipient.Execution.InvocationID != failed.Execution.InvocationID || deadRecipient.Execution.Attempts != g.cfg.ExpectedAttempts {
		return Report{}, fmt.Errorf("retry exhaustion changed invocation identity or used a different retry budget: %+v", deadRecipient.Execution)
	}
	if hooks.DeadLetter != nil {
		if err := hooks.DeadLetter(ctx); err != nil {
			return Report{}, err
		}
	}
	if err := g.recover(ctx, deadRecipient); err != nil {
		return Report{}, err
	}
	_, err = g.wait(ctx, func(h, f api.EventReceiptRecipientResponse) bool {
		return f.Execution != nil && f.Execution.State == "completed"
	})
	if err != nil {
		return Report{}, fmt.Errorf("wait for selective DLQ recovery: %w", err)
	}
	// Repeat publication after recovery and observe two drain ticks. Counts at
	// the application boundary catch dispatches that a stable row ID alone
	// would hide. Polling reads continue to assert the terminal states.
	if err := g.publish(ctx); err != nil {
		return Report{}, err
	}
	if err := pause(ctx, 2*time.Second); err != nil {
		return Report{}, err
	}
	final, err := g.receipt(ctx)
	if err != nil {
		return Report{}, err
	}
	return g.verify(ctx, final, baseline, failed.Execution.InvocationID)
}

func (g *gate) prepare(ctx context.Context) error {
	for _, consumer := range []struct {
		app, base string
		fail      bool
	}{{g.cfg.HealthyApp, g.cfg.HealthyURL, false}, {g.cfg.FailingApp, g.cfg.FailingURL, true}} {
		var subs api.EventSubscriptionListResponse
		if err := g.api(ctx, "GET", "/v1/apps/"+url.PathEscape(consumer.app)+"/event-subscriptions", nil, 200, &subs); err != nil {
			return err
		}
		id := ""
		for _, sub := range subs.Subscriptions {
			if sub.Enabled && sub.Source == g.cfg.Source && sub.Type == g.cfg.EventType {
				if id != "" {
					return fmt.Errorf("consumer %s has multiple matching acceptance subscriptions", consumer.app)
				}
				id = sub.ID
			}
		}
		if id == "" {
			return fmt.Errorf("consumer %s needs one enabled exact source/type subscription", consumer.app)
		}
		if consumer.fail {
			g.failingSubscription = id
		} else {
			g.healthySubscription = id
		}
		p := Preparation{Source: g.cfg.Source, EventID: g.cfg.EventID, EventType: g.cfg.EventType, SubscriptionID: id, Fail: consumer.fail}
		if err := g.control(ctx, consumer.base, "POST", "/__gate/events", p, nil); err != nil {
			return err
		}
	}
	return nil
}

func (g *gate) publish(ctx context.Context) error {
	var accepted api.PublishEventResponse
	p := api.PublishEventRequest{ID: g.cfg.EventID, Source: g.cfg.Source, Type: g.cfg.EventType, Data: json.RawMessage(`{"acceptance_gate":"independent_delivery_recovery"}`)}
	if err := g.api(ctx, "POST", "/v1/events:publish", p, 202, &accepted); err != nil {
		return err
	}
	if err := relativeAPIPath(accepted.ReceiptURL); err != nil {
		return err
	}
	if accepted.AcceptedAt.IsZero() || g.receiptURL != "" && (g.receiptURL != accepted.ReceiptURL || !g.acceptedAt.Equal(accepted.AcceptedAt)) {
		return fmt.Errorf("publish lost its stable acceptance identity or timestamp")
	}
	g.receiptURL = accepted.ReceiptURL
	g.acceptedAt = accepted.AcceptedAt
	return nil
}

func (g *gate) receipt(ctx context.Context) (api.EventReceiptResponse, error) {
	var receipt api.EventReceiptResponse
	err := g.api(ctx, "GET", g.receiptURL, nil, 200, &receipt)
	if err == nil && (receipt.EventID != g.cfg.EventID || receipt.EventSource != g.cfg.Source || !receipt.SnapshotCaptured || receipt.RecipientCount != 2 || len(receipt.Recipients) != 2) {
		err = fmt.Errorf("receipt must capture exactly the two configured consumers")
	}
	return receipt, err
}

func (g *gate) recipients(r api.EventReceiptResponse) (api.EventReceiptRecipientResponse, api.EventReceiptRecipientResponse, error) {
	var healthy, failed api.EventReceiptRecipientResponse
	for _, recipient := range r.Recipients {
		switch recipient.SubscriptionID {
		case g.healthySubscription:
			healthy = recipient
		case g.failingSubscription:
			failed = recipient
		}
	}
	if healthy.SubscriptionID == "" || failed.SubscriptionID == "" {
		return healthy, failed, fmt.Errorf("receipt lost a captured consumer")
	}
	return healthy, failed, nil
}

func (g *gate) wait(ctx context.Context, done func(api.EventReceiptRecipientResponse, api.EventReceiptRecipientResponse) bool) (api.EventReceiptResponse, error) {
	var last api.EventReceiptResponse
	for {
		r, err := g.receipt(ctx)
		if err != nil {
			return last, err
		}
		last = r
		h, f, err := g.recipients(r)
		if err != nil {
			return last, err
		}
		if done(h, f) {
			return last, nil
		}
		if h.Routing.State == "failed" || f.Routing.State == "failed" || h.Execution != nil && h.Execution.State != "pending" && h.Execution.State != "dispatching" && h.Execution.State != "completed" ||
			f.Execution != nil && (f.Execution.State == "failed" || f.Execution.State == "cancelled") {
			return last, fmt.Errorf("unexpected terminal delivery outcome: %+v", last)
		}
		if err := pause(ctx, 100*time.Millisecond); err != nil {
			return last, fmt.Errorf("%w; last receipt: %+v", err, last)
		}
	}
}

func (g *gate) recover(ctx context.Context, recipient api.EventReceiptRecipientResponse) error {
	var action api.EventReceiptRecoveryAction
	for _, candidate := range recipient.RecoveryActions {
		if candidate.Kind == "dead_letter_replay" {
			action = candidate
		}
	}
	if action.Method != "POST" || action.URL == "" {
		return fmt.Errorf("failed consumer has no selective dead-letter replay action")
	}
	if err := g.control(ctx, g.cfg.FailingURL, "POST", "/__gate/recover", Preparation{Source: g.cfg.Source, EventID: g.cfg.EventID}, nil); err != nil {
		return err
	}
	// The exact same idempotency key is retried while the scheduler is live.
	for range 2 {
		var dlq api.DeadLetterEvent
		if err := g.request(ctx, g.cfg.APIURL, "POST", action.URL, nil, 202, &dlq, map[string]string{"Authorization": "Bearer " + g.cfg.APIToken, "Idempotency-Key": "event-gate-" + g.cfg.EventID}); err != nil {
			return err
		}
		if dlq.Origin != "event_subscription" || dlq.SourceID != recipient.Execution.InvocationID || dlq.ReplayedAt == nil {
			return fmt.Errorf("DLQ replay did not preserve event invocation identity")
		}
	}
	return nil
}

func (g *gate) verify(ctx context.Context, receipt api.EventReceiptResponse, baseline api.EventReceiptExecutionResponse, failedID string) (Report, error) {
	report := Report{Receipt: receipt}
	h, f, err := g.recipients(receipt)
	if err != nil {
		return report, err
	}
	if receipt.RoutingMode != g.cfg.RoutingMode || receipt.RoutingSettledAt == nil || receipt.RoutingSummary["enqueued"] != 2 || h.Routing.State != "enqueued" || f.Routing.State != "enqueued" {
		return report, fmt.Errorf("routing did not settle in the expected mode: %+v", receipt)
	}
	if h.Execution == nil || !reflect.DeepEqual(*h.Execution, baseline) {
		return report, fmt.Errorf("successful sibling changed during recovery")
	}
	if f.Execution == nil || f.Execution.State != "completed" || f.Execution.InvocationID != failedID || f.Execution.ReplayGeneration != 1 || f.Execution.Attempts != 1 || len(f.RecoveryActions) != 0 {
		return report, fmt.Errorf("selective replay did not complete the original invocation exactly once: %+v", f)
	}
	query := "?" + url.Values{"source": {g.cfg.Source}, "event_id": {g.cfg.EventID}}.Encode()
	if err := g.control(ctx, g.cfg.HealthyURL, "GET", "/__gate/events"+query, nil, &report.Healthy); err != nil {
		return report, err
	}
	if err := g.control(ctx, g.cfg.FailingURL, "GET", "/__gate/events"+query, nil, &report.Recovered); err != nil {
		return report, err
	}
	if err := verifyConsumer(report.Healthy, baseline.InvocationID, 1, 0); err != nil {
		return report, fmt.Errorf("healthy sibling: %w", err)
	}
	if err := verifyConsumer(report.Recovered, failedID, g.cfg.ExpectedAttempts+1, g.cfg.ExpectedAttempts); err != nil {
		return report, fmt.Errorf("recovered consumer: %w", err)
	}
	report.HealthyHistory, err = g.history(ctx, h.AttemptHistoryURL)
	if err != nil {
		return report, err
	}
	report.FailureHistory, err = g.history(ctx, f.AttemptHistoryURL)
	if err != nil {
		return report, err
	}
	if err := verifyHistory(report.HealthyHistory, baseline.InvocationID, 0); err != nil {
		return report, err
	}
	if err := verifyHistory(report.FailureHistory, failedID, g.cfg.ExpectedAttempts); err != nil {
		return report, err
	}
	for _, id := range []string{g.healthySubscription, g.failingSubscription} {
		var backlog api.EventBacklogResponse
		if err := g.api(ctx, "GET", "/v1/events/backlog?subscription_id="+url.QueryEscape(id), nil, 200, &backlog); err != nil {
			return report, err
		}
		if len(backlog.Recipients) != 0 || len(backlog.Consumers) != 0 || backlog.UnattributedReceipts != 0 {
			return report, fmt.Errorf("settled delivery remains in routing backlog")
		}
	}
	report.Gate = "pass"
	return report, nil
}

func verifyConsumer(stats ConsumerStats, id string, attempts, failures int) error {
	if stats.Attempts != attempts || stats.Failures != failures || stats.SuccessfulResponses != 1 || stats.Effects != 1 || len(stats.InvocationIDs) != attempts {
		return fmt.Errorf("unexpected application execution evidence: %+v", stats)
	}
	for _, deliveredID := range stats.InvocationIDs {
		if deliveredID != id {
			return fmt.Errorf("handler received a different invocation identity")
		}
	}
	return nil
}

func verifyHistory(history []api.InvocationAttemptResponse, id string, failures int) error {
	if len(history) != failures+1 {
		return fmt.Errorf("attempt history has %d rows, want %d", len(history), failures+1)
	}
	// The public API paginates newest-first. Walk the returned sequence
	// backwards to check the original retry budget before the replay.
	for i := range history {
		attempt := history[len(history)-1-i]
		generation, number, outcome := int64(0), i+1, "retry"
		switch i {
		case failures:
			number, outcome = 1, "succeeded"
			if failures > 0 {
				generation = 1
			}
		case failures - 1:
			outcome = "dead_letter"
		}
		if attempt.InvocationID != id || attempt.ReplayGeneration != generation || attempt.Attempt != number || attempt.Outcome != outcome || attempt.FinishedAt == nil || attempt.RetainUntil.Before(*attempt.FinishedAt) {
			return fmt.Errorf("incorrect retained attempt %d: %+v", i, attempt)
		}
	}
	return nil
}

func (g *gate) history(ctx context.Context, path string) ([]api.InvocationAttemptResponse, error) {
	if path == "" {
		return nil, fmt.Errorf("receipt is missing attempt history")
	}
	u, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("limit", "1")
	var attempts []api.InvocationAttemptResponse
	for len(attempts) <= g.cfg.ExpectedAttempts+1 {
		u.RawQuery = query.Encode()
		var page api.EventReceiptAttemptHistoryResponse
		if err := g.api(ctx, "GET", u.String(), nil, 200, &page); err != nil {
			return nil, err
		}
		if len(page.Attempts) != 1 || page.OriginalInvocationID == "" {
			return nil, fmt.Errorf("attempt pagination lost evidence")
		}
		attempts = append(attempts, page.Attempts...)
		if page.NextAfter == "" {
			return attempts, nil
		}
		query.Set("after", page.NextAfter)
	}
	return nil, fmt.Errorf("attempt pagination did not terminate")
}

func pause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func relativeAPIPath(path string) error {
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/v1/") {
		return fmt.Errorf("receipt returned an invalid relative API URL")
	}
	return nil
}

func (g *gate) api(ctx context.Context, method, path string, body any, status int, result any) error {
	return g.request(ctx, g.cfg.APIURL, method, path, body, status, result, map[string]string{"Authorization": "Bearer " + g.cfg.APIToken})
}

func (g *gate) control(ctx context.Context, base, method, path string, body, result any) error {
	return g.request(ctx, base, method, path, body, 200, result, map[string]string{"X-Gate-Control-Token": g.cfg.ControlToken})
}

func (g *gate) request(ctx context.Context, base, method, path string, body any, want int, result any, headers map[string]string) error {
	if base == g.cfg.APIURL {
		if err := relativeAPIPath(path); err != nil {
			return err
		}
	}
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: status %d, want %d", method, req.URL.Path, resp.StatusCode, want)
	}
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode %s: %w", req.URL.Path, err)
	}
	return nil
}

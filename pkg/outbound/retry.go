package outbound

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	initialRetryDelay   = 100 * time.Millisecond
	maxRetryDelay       = time.Second
	maxProviderCooldown = time.Hour
)

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryableUpstreamError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE)
}

func retryAfterDelay(value string, now time.Time) (time.Duration, bool) {
	delay, ok := parseRetryAfter(value, now)
	if !ok {
		return 0, false
	}
	return min(delay, maxRetryDelay), true
}

// parseRetryAfter accepts the two RFC 9110 Retry-After forms and bounds
// provider-controlled state so one response cannot suppress an integration
// indefinitely. A past date is valid and means no delay.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	digitsOnly := true
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			digitsOnly = false
			break
		}
	}
	if digitsOnly {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(maxProviderCooldown/time.Second) {
			return maxProviderCooldown, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if !when.After(now) {
		return 0, true
	}
	delay := when.Sub(now)
	if delay > maxProviderCooldown {
		delay = maxProviderCooldown
	}
	return delay, true
}

func outboundRetryDelay(retryAfter string, retryNumber int, now time.Time) time.Duration {
	var delay time.Duration
	if parsed, ok := retryAfterDelay(retryAfter, now); ok {
		delay = parsed
	} else {
		shift := min(retryNumber, 10)
		delay = initialRetryDelay * time.Duration(1<<shift)
	}
	return min(delay, maxRetryDelay)
}

func waitForOutboundRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func canRetryRequest(req *http.Request) bool {
	if req == nil || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
		return false
	}
	for _, name := range []string{"X-HTTP-Method-Override", "X-Method-Override", "X-HTTP-Method"} {
		if req.Header.Get(name) != "" {
			return false
		}
	}
	if req.URL != nil && req.URL.Query().Has("_method") {
		return false
	}
	return req.Body == nil || req.Body == http.NoBody
}

// outboundCircuitOutcome counts one logical request after its bounded retry
// sequence. Provider 4xx responses other than transient retry statuses prove
// that the upstream is reachable and therefore reset the consecutive-failure
// streak; caller cancellation and local request-size errors are neutral.
func outboundCircuitOutcome(resp *http.Response, err error) CircuitBreakerOutcome {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return CircuitOutcomeNeutral
		}
		var maxBodyError *http.MaxBytesError
		if errors.As(err, &maxBodyError) {
			return CircuitOutcomeNeutral
		}
		if errors.Is(err, context.DeadlineExceeded) || retryableUpstreamError(err) {
			return CircuitOutcomeFailure
		}
		return CircuitOutcomeNeutral
	}
	if resp == nil {
		return CircuitOutcomeNeutral
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooEarly || resp.StatusCode == http.StatusTooManyRequests {
		return CircuitOutcomeFailure
	}
	return CircuitOutcomeSuccess
}

func (h *Handler) doWithRetries(
	ctx context.Context, req *http.Request, integrationID, metricIntegrationID string, policyRevision int64,
	maxRetries, retryBudgetPerMinute int,
) (*http.Response, int, error) {
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries > api.MaxOutboundRetries {
		maxRetries = api.MaxOutboundRetries
	}
	canRetry := canRetryRequest(req)
	attempts := 0
	retries := 0
	for {
		attempts++
		started := time.Now()
		resp, err := h.Client.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			h.Metrics.ObserveUpstreamError(metricIntegrationID, time.Since(started))
			if !canRetry || retries >= maxRetries || !retryableUpstreamError(err) {
				return nil, attempts, err
			}
			if waitErr := waitForOutboundRetry(ctx, outboundRetryDelay("", retries, time.Now())); waitErr != nil {
				return nil, attempts, waitErr
			}
			if !h.consumeRetryBudget(ctx, integrationID, metricIntegrationID, retryBudgetPerMinute) {
				return nil, attempts, err
			}
		} else {
			if resp == nil {
				err = errors.New("outbound transport returned an empty response")
				h.Metrics.ObserveUpstreamError(metricIntegrationID, time.Since(started))
				return nil, attempts, err
			}
			h.Metrics.ObserveUpstream(metricIntegrationID, resp.StatusCode, time.Since(started))
			retryAfterValue := resp.Header.Get("Retry-After")
			providerDelay, hasRetryAfter := parseRetryAfter(retryAfterValue, time.Now())
			if resp.StatusCode == http.StatusTooManyRequests && hasRetryAfter && providerDelay > 0 {
				h.recordProviderCooldown(ctx, integrationID, metricIntegrationID, policyRevision, providerDelay)
			}
			if !canRetry || retries >= maxRetries || !retryableStatus(resp.StatusCode) {
				return resp, attempts, nil
			}
			// Do not retry earlier than a provider's requested delay. Short waits
			// still fit the bounded retry window; longer waits return the original
			// provider response and let the caller retry later.
			if hasRetryAfter && providerDelay > maxRetryDelay {
				return resp, attempts, nil
			}
			delay := outboundRetryDelay(resp.Header.Get("Retry-After"), retries, time.Now())
			if deadline, ok := ctx.Deadline(); hasRetryAfter && ok && !time.Now().Add(delay).Before(deadline) {
				return resp, attempts, nil
			}
			if waitErr := waitForOutboundRetry(ctx, delay); waitErr != nil {
				if resp.Body != nil {
					_ = resp.Body.Close()
				}
				return nil, attempts, waitErr
			}
			if !h.consumeRetryBudget(ctx, integrationID, metricIntegrationID, retryBudgetPerMinute) {
				return resp, attempts, nil
			}
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
		retries++
		req = req.Clone(ctx)
		req.Body = http.NoBody
	}
}

func (h *Handler) recordProviderCooldown(ctx context.Context, integrationID, metricIntegrationID string, policyRevision int64, delay time.Duration) {
	backend, ok := h.Backend.(ProviderCooldownBackend)
	if !ok {
		h.Metrics.ObserveProviderCooldown(metricIntegrationID, "unavailable")
		return
	}
	observeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := backend.RecordProviderCooldown(observeCtx, integrationID, policyRevision, delay); err != nil {
		h.Metrics.ObserveProviderCooldown(metricIntegrationID, "state_error")
		return
	}
	h.Metrics.ObserveProviderCooldown(metricIntegrationID, "recorded")
}

func (h *Handler) consumeRetryBudget(
	ctx context.Context, integrationID, metricIntegrationID string, retryBudgetPerMinute int,
) bool {
	if retryBudgetPerMinute == 0 {
		return true
	}
	backend, ok := h.Backend.(RetryBudgetBackend)
	if !ok {
		h.Metrics.ObserveRetryBudget(metricIntegrationID, "unavailable")
		return false
	}
	allowed, err := backend.ConsumeRetryToken(ctx, integrationID, retryBudgetPerMinute)
	if err != nil {
		h.Metrics.ObserveRetryBudget(metricIntegrationID, "state_error")
		return false
	}
	if !allowed {
		h.Metrics.ObserveRetryBudget(metricIntegrationID, "exhausted")
		return false
	}
	h.Metrics.ObserveRetryBudget(metricIntegrationID, "consumed")
	return true
}

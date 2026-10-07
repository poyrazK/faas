// adr:684
package fcvm

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
)

// ReadImageHealthcheckConfig proves that this guest supports single-attempt
// runtime checks, and reads timing from the effective main image contract.
func ReadImageHealthcheckConfig(ctx context.Context, socket string) (healthcheckproto.Config, error) {
	var config healthcheckproto.Config
	nonce, err := imageHealthcheckExchange(ctx, socket, healthcheckproto.ConfigProbe, healthcheckproto.ConfigAck, "", &config)
	if err != nil {
		return config, err
	}
	if config.Nonce != nonce {
		return config, fmt.Errorf("healthcheck configuration challenge mismatch")
	}
	return config, config.Validate()
}

func checkImageHealthcheckOnce(ctx context.Context, socket, runtimeID string) (healthcheckproto.Response, error) {
	var response healthcheckproto.Response
	nonce, err := imageHealthcheckExchange(ctx, socket, healthcheckproto.CheckOnce, healthcheckproto.CheckAck, runtimeID, &response)
	if err != nil {
		return response, err
	}
	if response.Nonce != nonce {
		return response, fmt.Errorf("runtime healthcheck challenge mismatch")
	}
	if response.Error == "runtime_changed" || response.Error == "runtime_unavailable" {
		return response, nil
	}
	if response.RuntimeID != runtimeID || response.Healthy != (response.Error == "") {
		return response, fmt.Errorf("invalid runtime healthcheck response")
	}
	if response.NextIntervalNS < int64(api.OCIHealthcheckMinimumDuration) {
		return response, fmt.Errorf("invalid runtime healthcheck interval")
	}
	switch response.Error {
	case "", "unhealthy", "starting", "deadline", "healthcheck_missing", "invalid_request":
		return response, nil
	default:
		return response, fmt.Errorf("invalid runtime healthcheck outcome")
	}
}

type imageHealthcheckMonitor struct {
	config  func(context.Context) (healthcheckproto.Config, error)
	probe   func(context.Context, string) (healthcheckproto.Response, error)
	wait    func(context.Context, time.Duration) bool
	observe func(string, int, time.Duration)
}

// RunImageHealthcheckMonitor performs fresh, serialized command attempts until
// cancellation or a terminal threshold. Schedd remains the recovery owner.
// Missing/invalid transport proof is infrastructure recovery, never an app
// health failure. The caller must stop sibling probes on a terminal return.
func RunImageHealthcheckMonitor(ctx context.Context, socket string, observe func(string, int, time.Duration)) string {
	monitor := imageHealthcheckMonitor{
		config: func(ctx context.Context) (healthcheckproto.Config, error) {
			return ReadImageHealthcheckConfig(ctx, socket)
		},
		probe: func(ctx context.Context, runtime string) (healthcheckproto.Response, error) {
			return checkImageHealthcheckOnce(ctx, socket, runtime)
		},
		wait:    waitImageHealthcheckInterval,
		observe: observe,
	}
	return monitor.run(ctx)
}

func (m imageHealthcheckMonitor) run(ctx context.Context) string {
	var config healthcheckproto.Config
	failures, transportFailures := 0, 0
	for ctx.Err() == nil {
		delay := time.Duration(0)
		if config.RuntimeID != "" {
			delay = time.Duration(config.IntervalNS)
		}
		if transportFailures > 0 {
			delay = api.ImageHealthcheckTransportRetryInterval
		}
		if !m.wait(ctx, delay) {
			return ""
		}
		started := time.Now()
		outcome := "transport"
		if config.RuntimeID == "" {
			probeCtx, cancel := context.WithTimeout(ctx, api.ImageHealthcheckTransportAllowance)
			fresh, err := m.config(probeCtx)
			cancel()
			if err == nil && fresh.Validate() == nil {
				config = fresh
				transportFailures = 0
				continue
			}
		} else {
			probeCtx, cancel := context.WithTimeout(ctx, imageHealthcheckProbeBudget(time.Duration(config.TimeoutNS)))
			response, err := m.probe(probeCtx, config.RuntimeID)
			cancel()
			if err == nil {
				if response.NextIntervalNS > 0 {
					config.IntervalNS = response.NextIntervalNS
				}
				switch response.Error {
				case "":
					outcome = "healthy"
				case "unhealthy":
					outcome = "unhealthy"
				case "starting":
					outcome = "starting"
				case "runtime_changed", "runtime_unavailable":
					config = healthcheckproto.Config{}
				}
			}
		}
		if ctx.Err() != nil {
			return ""
		}
		if outcome == "transport" {
			failures = 0
			transportFailures++
		} else {
			transportFailures = 0
			if outcome == "unhealthy" {
				failures++
			} else {
				failures = 0
			}
		}
		if m.observe != nil {
			m.observe(outcome, failures, time.Since(started))
		}
		if outcome == "unhealthy" && failures >= config.Retries {
			return LivenessReasonImageHealthcheck
		}
		if transportFailures >= api.ImageHealthcheckTransportFailures {
			return LivenessReasonInfrastructure
		}
	}
	return ""
}

func imageHealthcheckProbeBudget(timeout time.Duration) time.Duration {
	const max = time.Duration(1<<63 - 1)
	if timeout > max-api.ImageHealthcheckTransportAllowance {
		return max
	}
	return timeout + api.ImageHealthcheckTransportAllowance
}

func waitImageHealthcheckInterval(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

package gregalemanifest

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type EventRoutingRetrySpec struct {
	MaxDeliveryAge   string `yaml:"max_delivery_age,omitempty" toml:"max_delivery_age"`
	MaxAttempts      int    `yaml:"max_attempts,omitempty" toml:"max_attempts"`
	MaxRetryDuration string `yaml:"max_retry_duration,omitempty" toml:"max_retry_duration"`
	InitialBackoff   string `yaml:"initial_backoff,omitempty" toml:"initial_backoff"`
	MaxBackoff       string `yaml:"max_backoff,omitempty" toml:"max_backoff"`
	Jitter           *bool  `yaml:"jitter,omitempty" toml:"jitter"`
}

func (t EventTrigger) RoutingRetryPolicy() (*api.EventRoutingRetryPolicy, error) {
	if t.Retry == nil {
		return nil, nil
	}
	p := api.DefaultEventRoutingRetryPolicy()
	p.Jitter = true
	if t.Retry.MaxAttempts != 0 {
		p.MaxAttempts = t.Retry.MaxAttempts
	}
	if t.Retry.Jitter != nil {
		p.Jitter = *t.Retry.Jitter
	}
	for _, field := range []struct {
		name, value string
		target      *int64
	}{{"max_delivery_age", t.Retry.MaxDeliveryAge, &p.MaxDeliveryAgeMS}, {"max_retry_duration", t.Retry.MaxRetryDuration, &p.MaxRetryDurationMS}, {"initial_backoff", t.Retry.InitialBackoff, &p.InitialBackoffMS}, {"max_backoff", t.Retry.MaxBackoff, &p.MaxBackoffMS}} {
		if field.value == "" {
			continue
		}
		duration, err := time.ParseDuration(field.value)
		if err != nil || duration < 0 || duration%time.Millisecond != 0 {
			return nil, fmt.Errorf("retry.%s must be a nonnegative whole-millisecond duration", field.name)
		}
		*field.target = duration.Milliseconds()
	}
	if t.Ordered && p.MaxDeliveryAgeMS > 0 {
		return nil, fmt.Errorf("ordered event delivery cannot configure max_delivery_age")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

package reposcan

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func composeHealthcheck(value any) (*api.ComposeHealthcheck, error) {
	if value == nil {
		return nil, nil
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("healthcheck must be a mapping")
	}
	check := &api.ComposeHealthcheck{}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		value := fields[key]
		switch key {
		case "test":
			test, err := composeHealthcheckTest(value)
			if err != nil {
				return nil, err
			}
			check.Test = test
		case "disable":
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("healthcheck disable must be a boolean")
			}
		case "retries":
			retries, ok := value.(int)
			if !ok || retries < 0 {
				return nil, fmt.Errorf("healthcheck retries must be a non-negative integer")
			}
			check.Retries = retries
		case "interval", "timeout", "start_period", "start_interval":
			raw, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("healthcheck %s must be a duration string", key)
			}
			duration, err := time.ParseDuration(raw)
			if err != nil {
				return nil, fmt.Errorf("healthcheck %s must be a valid duration", key)
			}
			switch key {
			case "interval":
				check.IntervalNS = int64(duration)
			case "timeout":
				check.TimeoutNS = int64(duration)
			case "start_period":
				check.StartPeriodNS = int64(duration)
			case "start_interval":
				check.StartIntervalNS = int64(duration)
			}
		default:
			if !strings.HasPrefix(key, "x-") {
				return nil, fmt.Errorf("unsupported healthcheck field %q", key)
			}
		}
	}
	if disabled, _ := fields["disable"].(bool); disabled {
		check.Test = []string{"NONE"}
	}
	if err := check.Validate(); err != nil {
		return nil, err
	}
	return check, nil
}

func composeHealthcheckTest(value any) ([]string, error) {
	switch test := value.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{"CMD-SHELL", strings.ReplaceAll(test, "$$", "$")}, nil
	case []any:
		if len(test) == 0 {
			return nil, nil
		}
		args := make([]string, len(test))
		for i, item := range test {
			arg, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("healthcheck test arguments must be strings")
			}
			args[i] = strings.ReplaceAll(arg, "$$", "$")
		}
		return args, nil
	default:
		return nil, fmt.Errorf("healthcheck test must be a string or argument list")
	}
}

package oci

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ApplyComposeHealthcheck merges a partial Compose override using Docker's
// zero/empty inheritance rules, without mutating the image config.
func ApplyComposeHealthcheck(config ImageConfig, override *api.ComposeHealthcheck) (ImageConfig, error) {
	if err := override.Validate(); err != nil {
		return ImageConfig{}, err
	}
	if override == nil {
		return config, nil
	}
	check := ImageHealthcheck{}
	timing := api.OCIHealthcheckTiming{}
	if config.Healthcheck != nil {
		check = *config.Healthcheck
		check.Test = append([]string(nil), check.Test...)
		if check.ImageTiming != nil {
			timing = *check.ImageTiming
		} else {
			timing = api.OCIHealthcheckTiming{IntervalNS: int64(check.IntervalS) * int64(time.Second),
				TimeoutNS: int64(check.TimeoutS) * int64(time.Second), StartPeriodNS: int64(check.StartPeriodS) * int64(time.Second)}
		}
	}
	if len(override.Test) > 0 {
		check.Test = append([]string(nil), override.Test...)
	}
	if override.IntervalNS != 0 {
		timing.IntervalNS = override.IntervalNS
	}
	if override.TimeoutNS != 0 {
		timing.TimeoutNS = override.TimeoutNS
	}
	if override.StartPeriodNS != 0 {
		timing.StartPeriodNS = override.StartPeriodNS
	}
	if override.StartIntervalNS != 0 {
		timing.StartIntervalNS = override.StartIntervalNS
	}
	if override.Retries != 0 {
		check.Retries = override.Retries
	}
	check.ImageTiming = &timing
	config.Healthcheck = &check
	return config, nil
}

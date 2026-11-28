package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// doctorBootImageHealthcheck returns the image's HEALTHCHECK when Docker will
// run it, nil for none or NONE.
func doctorBootImageHealthcheck(hc *api.AppManifestHealthcheck) *api.AppManifestHealthcheck {
	if hc == nil || len(hc.Test) == 0 || hc.Test[0] == "NONE" {
		return nil
	}
	return hc
}

// doctorBootHealthArgs makes Docker check every second, so a verdict arrives
// within the boot, and scales the retries so the failure budget (retries x
// interval) stays what the image and Gregale allow: an image checked every
// 30 s with 3 retries still gets 90 s of failures before it is unhealthy.
// The start period is unchanged. Defaults are Docker's, which guest-init
// uses too: 30 s interval, 3 retries.
func doctorBootHealthArgs(hc *api.AppManifestHealthcheck) []string {
	interval := time.Duration(hc.IntervalS) * time.Second
	if hc.ImageTiming != nil {
		interval = time.Duration(hc.ImageTiming.IntervalNS)
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	retries := hc.Retries
	if retries <= 0 {
		retries = 3
	}
	budget := time.Duration(retries) * interval
	scaled := int((budget + time.Second - 1) / time.Second)
	return []string{"--health-interval", "1s", "--health-retries", strconv.Itoa(max(scaled, 1))}
}

// doctorBootHealthState is the part of Docker's `.State.Health` the boot
// reports.
type doctorBootHealthState struct {
	Status string `json:"Status"`
	Log    []struct {
		ExitCode int    `json:"ExitCode"`
		Output   string `json:"Output"`
	} `json:"Log"`
}

// lastOutput is the most recent check's result, for the report.
func (h *doctorBootHealthState) lastOutput() string {
	if n := len(h.Log); n > 0 {
		return truncateBootText(strings.TrimSpace(fmt.Sprintf("exit %d: %s", h.Log[n-1].ExitCode, h.Log[n-1].Output)), 2000)
	}
	return ""
}

func (d doctorDocker) health(ctx context.Context, name string) (*doctorBootHealthState, error) {
	out, err := d.run(ctx, "inspect", "--format", "{{json .State.Health}}", name)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return nil, nil
	}
	var h doctorBootHealthState
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		return nil, fmt.Errorf("%s inspect: unexpected health output %q", d.bin, truncateBootText(out, 100))
	}
	return &h, nil
}

// imageHealth waits for the image HEALTHCHECK to pass, until the startup
// timeout. Docker turns an unhealthy container healthy again after a passing
// check, so only the status at the deadline decides a failure.
func (b doctorBoot) imageHealth(ctx context.Context) doctorCheck {
	deadline := b.started.Add(doctorBootStartupTimeout)
	for {
		h, err := b.docker.health(ctx, b.name)
		if err != nil {
			return doctorCheck{Name: "boot-healthcheck", Status: "error", Code: "boot_inspect_failed", Hint: err.Error()}
		}
		if h == nil {
			return doctorCheck{Name: "boot-healthcheck", Status: "skipped", Reason: "Docker did not run the image HEALTHCHECK."}
		}
		if h.Status == "healthy" {
			return doctorCheck{Name: "boot-healthcheck", Status: "ok", Detail: fmt.Sprintf("image HEALTHCHECK healthy after %s", bootSeconds(time.Since(b.started)))}
		}
		if time.Now().After(deadline) {
			return b.imageHealthVerdict(h)
		}
		select {
		case <-ctx.Done():
			return doctorCheck{Name: "boot-healthcheck", Status: "skipped", Reason: "interrupted"}
		case <-time.After(doctorBootPoll):
		}
	}
}

func (b doctorBoot) imageHealthVerdict(h *doctorBootHealthState) doctorCheck {
	if h.Status == "unhealthy" {
		return doctorCheck{Name: "boot-healthcheck", Status: "error", Code: "boot_healthcheck_unhealthy", Output: h.lastOutput(),
			Hint: fmt.Sprintf("The image HEALTHCHECK used up its retries and was still failing after %s; where image health checks are enabled, Gregale would restart or never route to the instance.", bootSeconds(doctorBootStartupTimeout)),
			Fix:  "Run the HEALTHCHECK command in the container (`docker exec`) to see why it fails; the tools it calls (curl, wget) must be in the image."}
	}
	return doctorCheck{Name: "boot-healthcheck", Status: "warn", Output: h.lastOutput(),
		Hint: fmt.Sprintf("The image HEALTHCHECK had not passed after %s (Docker status %q); it is within its retries, so it may still pass later.", bootSeconds(doctorBootStartupTimeout), h.Status),
		Fix:  "Check the HEALTHCHECK command and start period; a check that passes only later delays readiness on Gregale too."}
}

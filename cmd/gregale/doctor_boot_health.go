package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// doctorBootHealthState is the part of Docker's `.State.Health` the boot
// reports.
type doctorBootHealthState struct {
	Status string `json:"Status"`
	Log    []struct {
		ExitCode int    `json:"ExitCode"`
		Output   string `json:"Output"`
	} `json:"Log"`
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

// imageHealth waits for Docker's verdict on the image HEALTHCHECK, until the
// startup timeout. Docker counts failures only after the image's start
// period, as Gregale does.
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
		switch h.Status {
		case "healthy":
			return doctorCheck{Name: "boot-healthcheck", Status: "ok", Detail: fmt.Sprintf("image HEALTHCHECK healthy after %s", bootSeconds(time.Since(b.started)))}
		case "unhealthy":
			c := doctorCheck{Name: "boot-healthcheck", Status: "error", Code: "boot_healthcheck_unhealthy",
				Hint: "The image HEALTHCHECK failed its retries; where image health checks are enabled, Gregale would restart or never route to the instance.",
				Fix:  "Run the HEALTHCHECK command in the container (`docker exec`) to see why it fails; the tools it calls (curl, wget) must be in the image."}
			if n := len(h.Log); n > 0 {
				c.Output = truncateBootText(strings.TrimSpace(fmt.Sprintf("exit %d: %s", h.Log[n-1].ExitCode, h.Log[n-1].Output)), 2000)
			}
			return c
		}
		if time.Now().After(deadline) {
			return doctorCheck{Name: "boot-healthcheck", Status: "warn", Hint: fmt.Sprintf("The image HEALTHCHECK had not passed after %s (Docker status %q).", bootSeconds(doctorBootStartupTimeout), h.Status), Fix: "Check the HEALTHCHECK start period and command; a check that passes only later delays readiness on Gregale too."}
		}
		select {
		case <-ctx.Done():
			return doctorCheck{Name: "boot-healthcheck", Status: "skipped", Reason: "interrupted"}
		case <-time.After(doctorBootPoll):
		}
	}
}

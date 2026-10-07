package api

import (
	"fmt"
	"slices"
	"strings"
)

// ComposeHealthcheck is a partial override of an image's OCI HEALTHCHECK.
// Empty tests and zero timing/retry values inherit the artifact's settings.
// Test ["NONE"] explicitly disables the inherited check.
type ComposeHealthcheck struct {
	Test            []string `json:"test,omitempty"`
	IntervalNS      int64    `json:"interval_ns,omitempty"`
	TimeoutNS       int64    `json:"timeout_ns,omitempty"`
	StartPeriodNS   int64    `json:"start_period_ns,omitempty"`
	StartIntervalNS int64    `json:"start_interval_ns,omitempty"`
	Retries         int      `json:"retries,omitempty"`
}

func (h *ComposeHealthcheck) Clone() *ComposeHealthcheck {
	if h == nil {
		return nil
	}
	copy := *h
	copy.Test = slices.Clone(h.Test)
	return &copy
}

func (h *ComposeHealthcheck) Validate() error {
	if h == nil {
		return nil
	}
	if err := (OCIHealthcheckTiming{IntervalNS: h.IntervalNS, TimeoutNS: h.TimeoutNS,
		StartPeriodNS: h.StartPeriodNS, StartIntervalNS: h.StartIntervalNS}).Validate(); err != nil {
		return err
	}
	if h.Retries < 0 {
		return fmt.Errorf("healthcheck retries must be non-negative")
	}
	if len(h.Test) == 0 {
		return nil
	}
	for _, arg := range h.Test {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("healthcheck test must not contain NUL")
		}
	}
	switch h.Test[0] {
	case "NONE":
		if len(h.Test) == 1 {
			return nil
		}
	case "CMD":
		if len(h.Test) >= 2 && strings.TrimSpace(h.Test[1]) != "" {
			return nil
		}
	case "CMD-SHELL":
		if len(h.Test) == 2 && strings.TrimSpace(h.Test[1]) != "" {
			return nil
		}
	}
	return fmt.Errorf("healthcheck test must be NONE, CMD with arguments, or CMD-SHELL with one command")
}

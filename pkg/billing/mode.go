package billing

import (
	"fmt"
	"strings"
)

const BillingModeEnv = "FAAS_BILLING_MODE"

// Mode is the deployment-wide billing switch shared by apid and meterd.
// Empty remains live for deployments created before this switch existed.
type Mode string

const (
	ModeLive     Mode = "live"
	ModeDisabled Mode = "disabled"
)

func ParseMode(raw string) (Mode, error) {
	switch strings.TrimSpace(raw) {
	case "", string(ModeLive):
		return ModeLive, nil
	case string(ModeDisabled):
		return ModeDisabled, nil
	default:
		return "", fmt.Errorf("%s must be %q or %q (got %q)", BillingModeEnv, ModeDisabled, ModeLive, raw)
	}
}

func ModeFromEnv(getenv func(string) string) (Mode, error) {
	if getenv == nil {
		return ModeLive, nil
	}
	return ParseMode(getenv(BillingModeEnv))
}

func (m Mode) Enabled() bool { return m != ModeDisabled }

func (m Mode) Effective() Mode {
	if m == "" {
		return ModeLive
	}
	return m
}

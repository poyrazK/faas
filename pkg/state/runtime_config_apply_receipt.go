package state

import (
	"fmt"
	"regexp"
	"time"
)

// RuntimeConfigApplyReceiptStatus is the outcome a daemon instance reports
// for one runtime-configuration version.
type RuntimeConfigApplyReceiptStatus string

const (
	RuntimeConfigReceiptApplied RuntimeConfigApplyReceiptStatus = "applied"
	RuntimeConfigReceiptFailed  RuntimeConfigApplyReceiptStatus = "failed"
)

// RuntimeConfigApplyReceipt (ADR-1700) records which version of a hot
// runtime-configuration key one daemon instance applied, or why it could not.
// It is distinct from instance runtime-config receipts, which describe the
// inputs a microVM booted with.
type RuntimeConfigApplyReceipt struct {
	Key        string
	Scope      RuntimeConfigScope
	ScopeID    string
	Daemon     string
	Instance   string
	Version    int64
	Status     RuntimeConfigApplyReceiptStatus
	Error      string
	ReportedAt time.Time
}

var runtimeConfigReceiptDaemonPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

const runtimeConfigReceiptMaxError = 1024

// normalizeRuntimeConfigApplyReceipt applies defaults and the table's CHECK
// constraints so both stores accept and reject the same receipts.
func normalizeRuntimeConfigApplyReceipt(r RuntimeConfigApplyReceipt) (RuntimeConfigApplyReceipt, error) {
	if r.Scope == "" {
		r.Scope = RuntimeConfigScopeGlobal
	}
	switch {
	case r.Key == "" || len(r.Key) > 128:
		return r, fmt.Errorf("state: runtime config receipt key must be 1..128 bytes")
	case !runtimeConfigReceiptDaemonPattern.MatchString(r.Daemon):
		return r, fmt.Errorf("state: runtime config receipt daemon %q is invalid", r.Daemon)
	case r.Instance == "" || len(r.Instance) > 255:
		return r, fmt.Errorf("state: runtime config receipt instance must be 1..255 bytes")
	case r.Version < 0:
		return r, fmt.Errorf("state: runtime config receipt version must not be negative")
	case r.Status != RuntimeConfigReceiptApplied && r.Status != RuntimeConfigReceiptFailed:
		return r, fmt.Errorf("state: runtime config receipt status %q is invalid", r.Status)
	}
	switch r.Scope {
	case RuntimeConfigScopeGlobal, RuntimeConfigScopeControlPlane, RuntimeConfigScopeDaemon, RuntimeConfigScopeNode:
	default:
		return r, fmt.Errorf("state: runtime config receipt scope %q is invalid", r.Scope)
	}
	if r.Status == RuntimeConfigReceiptApplied {
		r.Error = ""
	}
	if len(r.Error) > runtimeConfigReceiptMaxError {
		r.Error = r.Error[:runtimeConfigReceiptMaxError]
	}
	return r, nil
}

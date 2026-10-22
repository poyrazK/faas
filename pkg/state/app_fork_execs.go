package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppForkExecStatus is the ADR-732 fork exec lifecycle: queued → running →
// succeeded | failed | timed_out.
type AppForkExecStatus string

const (
	AppForkExecQueued    AppForkExecStatus = "queued"
	AppForkExecRunning   AppForkExecStatus = "running"
	AppForkExecSucceeded AppForkExecStatus = "succeeded"
	AppForkExecFailed    AppForkExecStatus = "failed"
	AppForkExecTimedOut  AppForkExecStatus = "timed_out"
)

// Terminal reports whether no further transition happens.
func (s AppForkExecStatus) Terminal() bool {
	return s == AppForkExecSucceeded || s == AppForkExecFailed || s == AppForkExecTimedOut
}

// AppForkExec is one command run inside a running fork.
type AppForkExec struct {
	ID              string
	ForkID          string
	AccountID       string
	AppID           string
	RequestedBy     string
	Command         []string
	CommandShell    bool
	TimeoutSeconds  int
	MaxOutputBytes  int
	Status          AppForkExecStatus
	ExitCode        *int
	OutputTruncated bool
	Stdout          []byte
	Stderr          []byte
	FailureCode     *string
	FailureMessage  *string
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	UpdatedAt       time.Time
}

// CreateAppForkExecParams records a command for a running fork.
type CreateAppForkExecParams struct {
	AccountID, AppID, ForkID string
	RequestedBy              string
	Command                  []string
	CommandShell             bool
	TimeoutSeconds           int
	MaxOutputBytes           int
	MaxPending               int
	CreatedAt                time.Time
}

// FinishAppForkExecParams records a command's terminal result.
type FinishAppForkExecParams struct {
	ID              string
	Status          AppForkExecStatus
	ExitCode        *int
	OutputTruncated bool
	Stdout, Stderr  []byte
	FailureCode     string
	FailureMessage  string
	FinishedAt      time.Time
}

// ErrAppForkExecRefused means the fork is not running (or ending), or
// already has the maximum number of pending commands.
var ErrAppForkExecRefused = errors.New("state: fork exec refused")

// AppForkExecStore is the ADR-732 fork exec surface: apid creates and reads,
// the schedd holding the fork's lease claims and finishes.
type AppForkExecStore interface {
	CreateAppForkExec(ctx context.Context, params CreateAppForkExecParams) (AppForkExec, error)
	AppForkExecByID(ctx context.Context, accountID, appID, forkID, execID string) (AppForkExec, error)
	ListAppForkExecs(ctx context.Context, accountID, appID, forkID string, limit int) ([]AppForkExec, error)
	ClaimNextAppForkExec(ctx context.Context, owner string, now time.Time) (AppForkExec, error)
	FinishAppForkExec(ctx context.Context, params FinishAppForkExecParams) (AppForkExec, error)
	// FailOrphanedAppForkExecs ends commands whose fork is no longer
	// running, or that ran past their timeout plus grace.
	FailOrphanedAppForkExecs(ctx context.Context, grace time.Duration, now time.Time) ([]AppForkExec, error)
}

func validateCreateAppForkExec(p CreateAppForkExecParams) (CreateAppForkExecParams, error) {
	p.RequestedBy = strings.TrimSpace(p.RequestedBy)
	total := 0
	for _, arg := range p.Command {
		total += len(arg)
		if strings.ContainsRune(arg, '\x00') || len(arg) > 4096 {
			return p, fmt.Errorf("%w: invalid command argument", ErrAppForkInvalid)
		}
	}
	switch {
	case p.AccountID == "" || p.AppID == "" || p.ForkID == "":
		return p, fmt.Errorf("%w: account, app and fork are required", ErrAppForkInvalid)
	case p.RequestedBy == "" || len(p.RequestedBy) > 256:
		return p, fmt.Errorf("%w: requested_by must be 1..256 bytes", ErrAppForkInvalid)
	case len(p.Command) == 0 || len(p.Command) > 64 || strings.TrimSpace(p.Command[0]) == "" || total > 16384:
		return p, fmt.Errorf("%w: command must have 1..64 arguments", ErrAppForkInvalid)
	case p.CommandShell && len(p.Command) != 1:
		return p, fmt.Errorf("%w: a shell command is one string", ErrAppForkInvalid)
	case p.TimeoutSeconds < 1 || p.TimeoutSeconds > 3600:
		return p, fmt.Errorf("%w: timeout_seconds outside 1..3600", ErrAppForkInvalid)
	case p.MaxOutputBytes < 1024 || p.MaxOutputBytes > 16<<20:
		return p, fmt.Errorf("%w: max_output_bytes outside 1024..16777216", ErrAppForkInvalid)
	case p.MaxPending < 1 || p.CreatedAt.IsZero():
		return p, fmt.Errorf("%w: max_pending and created_at are required", ErrAppForkInvalid)
	}
	p.CreatedAt = p.CreatedAt.UTC().Truncate(time.Microsecond)
	return p, nil
}

func (p FinishAppForkExecParams) validate() error {
	if p.ID == "" || !p.Status.Terminal() || p.FinishedAt.IsZero() || (p.FailureCode == "") != (p.FailureMessage == "") {
		return fmt.Errorf("%w: invalid fork exec result", ErrAppForkInvalid)
	}
	return nil
}

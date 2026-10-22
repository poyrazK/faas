//go:build !unix

package wire

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// HandoffOptions configures Handoff; hand-offs need a unix host.
type HandoffOptions struct {
	ReadyTimeout time.Duration
	Path         string
	Args         []string
	Log          *slog.Logger
	Checks       []HandoffCheck
}

// HandoffRequests never delivers off unix.
func HandoffRequests(context.Context) <-chan struct{} { return nil }

// IsHandoffSuccessor is always false off unix.
func IsHandoffSuccessor() bool { return false }

// Handoff is unsupported off unix.
func Handoff(context.Context, HandoffOptions) (int, error) {
	return 0, errors.New("wire: hand-off requires a unix host")
}

const handoffFDsEnv = "FAAS_HANDOFF_FDS"

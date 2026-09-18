//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/onebox-faas/faas/pkg/extension"
)

// extensionLifecycle is the guest-init seam for the versioned extension
// protocol. Hooks are best-effort: an optional observability extension must
// never make an app fail to boot, resume, or shut down.
type extensionLifecycle struct {
	dispatcher *extension.Dispatcher
	log        *slog.Logger
}

func newExtensionLifecycle(log *slog.Logger) *extensionLifecycle {
	if log == nil {
		log = slog.Default()
	}
	path := os.Getenv("FAAS_EXTENSION_SOCKET")
	return &extensionLifecycle{
		dispatcher: extension.NewDispatcher(path),
		log:        log,
	}
}

func (l *extensionLifecycle) emit(phase extension.Phase) {
	if l == nil || l.dispatcher == nil {
		return
	}
	if err := l.dispatcher.Dispatch(context.Background(), phase, nil); err != nil {
		if errors.Is(err, extension.ErrUnavailable) {
			l.log.Debug("extension lifecycle hook unavailable", "phase", phase)
			return
		}
		l.log.Warn("extension lifecycle hook failed", "phase", phase, "err", err)
	}
}

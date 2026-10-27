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
	_ = l.emitWithMetadata(phase, nil)
}

// emitWithMetadata returns the dispatch error, already logged, so the vsock
// bridge can tell the host that no extension endpoint is listening.
func (l *extensionLifecycle) emitWithMetadata(phase extension.Phase, metadata map[string]string) error {
	if l == nil || l.dispatcher == nil {
		return extension.ErrUnavailable
	}
	err := l.dispatcher.Dispatch(context.Background(), phase, metadata)
	if err != nil {
		if errors.Is(err, extension.ErrUnavailable) {
			l.log.Debug("extension lifecycle hook unavailable", "phase", phase)
		} else {
			l.log.Warn("extension lifecycle hook failed", "phase", phase, "err", err)
		}
	}
	return err
}

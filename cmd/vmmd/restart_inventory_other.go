//go:build !linux

package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// Portable builds have no Linux guest resources. Linux wiring is mandatory.
func recoverRestartResources(context.Context, *fcvm.Manager, string, *slog.Logger) error {
	return nil
}

package state

import (
	"context"
	"time"
)

// AppBillingWindowStore retains internal billing membership after soft deletion.
// Customer-facing app lists deliberately exclude these historical owners.
type AppBillingWindowStore interface {
	ListDeletedAppsInBillingWindow(context.Context, time.Time, time.Time) ([]App, error)
}

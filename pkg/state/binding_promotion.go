// adr: 429 — a promotion compares binding facts at the traffic write boundary.
package state

import (
	"context"
	"errors"
	"time"
)

var (
	ErrBindingPromotionChanged = errors.New("binding promotion observations changed")
	ErrBindingPromotionExpired = errors.New("binding promotion evidence expired")
)

// These tokens are internal and cannot be supplied by a public API caller.
type BindingPromotionFence struct {
	AccountID, AppID, DeploymentID, Scope, Revision string
	ValidUntil                                      time.Time
}

type BindingPromotionResult struct {
	Deployment  Deployment
	FromPercent int
	CheckedAt   time.Time
}

type BindingPromotionStore interface {
	ReadBindingPromotionRevision(context.Context, string, string) (string, error)
	BindingPromotionBackend() any
	PromoteDeploymentWithBindings(context.Context, string, BindingPromotionFence, string) (BindingPromotionResult, error)
}

type bindingTrafficGuard struct {
	fence       BindingPromotionFence
	snapshot    Deployment
	checkedAt   time.Time
	fromPercent int
}

func (g *bindingTrafficGuard) check(deployment Deployment, accountID, revision string, now time.Time) error {
	f := g.fence
	if accountID != f.AccountID || deployment.AppID != f.AppID || !sameDeploymentID(deployment.ID, f.DeploymentID) || normalizedDeploymentScope(deployment.Scope) != f.Scope ||
		deployment.RootfsKey == "" || deployment.ImageDigest == "" || f.Revision == "" || revision != f.Revision {
		return ErrBindingPromotionChanged
	}
	if !f.ValidUntil.IsZero() && now.After(f.ValidUntil) {
		return ErrBindingPromotionExpired
	}
	g.checkedAt, g.fromPercent = now.UTC(), deployment.TrafficPercent
	return nil
}

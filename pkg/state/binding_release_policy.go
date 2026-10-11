package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrBindingReleasePolicyRevision = errors.New("binding release policy revision changed")
var ErrBindingReleaseRequired = errors.New("binding release policy requires checked evidence before increasing traffic")

type BindingReleasePolicyStore interface {
	GetBindingReleasePolicy(context.Context, string, string, string) (api.BindingReleasePolicy, error)
	SetBindingReleasePolicy(context.Context, string, string, string, api.SetBindingReleasePolicyRequest) (api.BindingReleasePolicy, error)
}

// Only APID creates these after evaluating catalogs. Public request data never
// becomes a fence. Multiple fences cover every recipient of redistributed traffic.
type bindingReleaseFencesKey struct{}

func WithBindingReleaseFences(ctx context.Context, fences []BindingPromotionFence) context.Context {
	return context.WithValue(ctx, bindingReleaseFencesKey{}, append([]BindingPromotionFence(nil), fences...))
}
func bindingReleaseFences(ctx context.Context) []BindingPromotionFence {
	fences, _ := ctx.Value(bindingReleaseFencesKey{}).([]BindingPromotionFence)
	return fences
}

func IsBindingReleaseRequired(err error) bool {
	var pgErr *pgconn.PgError
	return errors.Is(err, ErrBindingReleaseRequired) || errors.As(err, &pgErr) && pgErr.ConstraintName == "binding_release_required"
}

func defaultBindingReleasePolicy(appID, scope string) api.BindingReleasePolicy {
	return api.BindingReleasePolicy{AppID: appID, Scope: normalizedDeploymentScope(scope), Mode: "off", MaxVerificationAge: api.DefaultBindingVerificationAge.String()}
}

func releaseFenceMatchesPolicy(f BindingPromotionFence, p api.BindingReleasePolicy) bool {
	age, err := time.ParseDuration(p.MaxVerificationAge)
	return err == nil && f.PolicyRevision == p.Revision && f.MaxVerificationAge > 0 && f.MaxVerificationAge <= age && !f.AllowUnsupported && (!p.RequireApplicationAck || f.RequireApplicationAck)
}

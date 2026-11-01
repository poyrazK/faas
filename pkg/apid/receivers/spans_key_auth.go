package receivers

import (
	"context"
	"errors"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// traceKeyPrincipal decides what an authenticated key may write through the
// customer OTLP endpoint (tracing tenancy T5, 2026-10-31 logs/traces audit),
// as apid decides for its own key-authenticated ingest routes:
//
//   - the key needs a scope that writes telemetry (api.ScopesTracesWriteSurface);
//   - the tracing capability must be released to the key's subject, the one
//     apid's capability gate decides GET /v1/account/traces for;
//   - an app-bound key writes only its app, under the app's resource owner's
//     plan;
//   - a key bound to a company writes that company's apps, whoever created
//     them, only while its holder may deploy there (owner, admin or
//     developer, as event publishing requires), under the company's plan
//     once it owns its resources.
//
// Any other key writes its holder's own apps under the holder's plan; the
// writer still refuses a company app the holder no longer works in.
func (r *authReceiver) traceKeyPrincipal(ctx context.Context, acct state.Account, key state.APIKey) (*apidpb.AuthenticateKeyResponse, error) {
	if !traceKeyScoped(key) {
		return nil, status.Error(codes.PermissionDenied, "api key cannot write traces: it needs one of admin, deploy:write or metrics:write")
	}
	if r.tracing != nil && !r.tracing(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: key.OrgID}) {
		return nil, status.Error(codes.NotFound, "tracing is not released to this account")
	}
	resp := &apidpb.AuthenticateKeyResponse{AccountId: acct.ID, Plan: string(acct.Plan)}
	switch {
	case key.AppID != "":
		return r.appKeyPrincipal(ctx, key, resp)
	case key.OrgID != "":
		return r.companyKeyPrincipal(ctx, acct, key, resp)
	}
	return resp, nil
}

// traceKeyScoped reports whether the key carries a scope that writes traces.
func traceKeyScoped(key state.APIKey) bool {
	for _, scope := range key.Scopes {
		if slices.Contains(api.ScopesTracesWriteSurface, scope) {
			return true
		}
	}
	return false
}

// appKeyPrincipal confines an app-bound key to its app, under the plan of the
// app's resource owner.
func (r *authReceiver) appKeyPrincipal(ctx context.Context, key state.APIKey, resp *apidpb.AuthenticateKeyResponse) (*apidpb.AuthenticateKeyResponse, error) {
	resp.AppId = key.AppID
	owners, ok := r.store.(telemetryOwners)
	if !ok {
		return resp, nil
	}
	app, err := owners.AppByID(ctx, key.AppID)
	if err != nil {
		return nil, traceKeyLookupError(err, "the key's app no longer exists")
	}
	owner, err := state.AppResourceAccount(ctx, owners, app)
	if err != nil {
		return nil, traceKeyLookupError(err, "the key's app has no resource owner")
	}
	resp.Plan = string(owner.Plan)
	return resp, nil
}

// companyKeyPrincipal resolves a key bound to an organization. A personal
// organization's key is the holder's own; a company's key writes the
// company's apps while its holder may deploy there.
func (r *authReceiver) companyKeyPrincipal(ctx context.Context, acct state.Account, key state.APIKey, resp *apidpb.AuthenticateKeyResponse) (*apidpb.AuthenticateKeyResponse, error) {
	owners, ok := r.store.(telemetryOwners)
	if !ok {
		return nil, status.Error(codes.Internal, "organization lookup unavailable")
	}
	org, err := owners.OrgByID(ctx, key.OrgID)
	if err != nil {
		return nil, traceKeyLookupError(err, "the key's organization no longer exists")
	}
	if org.Personal {
		return resp, nil
	}
	if org.DeletedPending || org.Status == state.OrgStatusDeletedPending {
		return nil, status.Error(codes.PermissionDenied, "the key's organization is being deleted")
	}
	access, ok := r.store.(state.OrgAccessStore)
	if !ok {
		return nil, status.Error(codes.Internal, "organization access lookup unavailable")
	}
	member, err := access.OrgAccessSnapshot(ctx, org.ID, acct.ID)
	if err != nil {
		return nil, traceKeyLookupError(err, "the key's holder is no longer a member of its organization")
	}
	if !traceKeyCompanyRole(member.Role) {
		return nil, status.Error(codes.PermissionDenied, "the key's holder cannot deploy to its organization's apps")
	}
	resp.OrgId = org.ID
	if org.ResourceOwnershipEnabled {
		owner, err := state.BillingResourceAccount(ctx, owners, org.ID)
		if err != nil {
			return nil, traceKeyLookupError(err, "the key's organization has no resource owner")
		}
		resp.Plan = string(owner.Plan)
	}
	return resp, nil
}

// traceKeyCompanyRole is the role that may write a company's trace evidence:
// the one that may deploy code that writes it (eventProducerRole in
// pkg/apid/appevents; the spans writer's SQL applies the same set).
func traceKeyCompanyRole(role state.OrgRole) bool {
	return role == state.OrgRoleOwner || role == state.OrgRoleAdmin || role == state.OrgRoleDeveloper
}

// traceKeyLookupError maps a missing row to PermissionDenied and anything
// else to Internal.
func traceKeyLookupError(err error, denied string) error {
	if errors.Is(err, state.ErrNotFound) {
		return status.Error(codes.PermissionDenied, denied)
	}
	return status.Errorf(codes.Internal, "apid trace key lookup: %v", err)
}

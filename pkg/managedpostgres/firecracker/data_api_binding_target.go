package firecracker

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.DataAPIBindingTargetProvider = (*Provider)(nil)

var dataAPITargetDNSLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// DataAPIBindingTarget reproduces the native provider's pooled Data API
// coordinates without provisioning or reading a password. The opaque stored
// provider identity is interpreted only by this provider, which created it.
func (p *Provider) DataAPIBindingTarget(ctx context.Context, req managedpostgres.CredentialRequest, providerIdentity string, generation int64) (managedpostgres.DataAPIBindingTarget, error) {
	var target managedpostgres.DataAPIBindingTarget
	if err := ctx.Err(); err != nil {
		return target, err
	}
	if p == nil || !p.cfg.DataAPI || p.cfg.TLSMode != "verify-full" {
		return target, managedpostgres.ErrUnsupported
	}
	resource, resourceErr := uuid.Parse(req.ProviderResourceID)
	binding, bindingErr := uuid.Parse(req.BindingID)
	if resourceErr != nil || resource == uuid.Nil || resource.String() != req.ProviderResourceID || bindingErr != nil || binding == uuid.Nil || binding.String() != req.BindingID || !managedpostgres.ValidDataAPIBindingCredentialRequest(req, generation) {
		return target, managedpostgres.ErrInvalid
	}
	login := credentialRoleName(req.ProviderResourceID, req.IdentityKey, req.Access)
	if providerIdentity != login {
		return target, managedpostgres.ErrConflict
	}
	host := req.ProviderResourceID + "-pooler." + p.cfg.HostSuffix
	if len(host) > 253 || p.cfg.Port == 0 || !identifierPattern.MatchString(p.cfg.Database) || p.cfg.Database == "postgres" {
		return target, managedpostgres.ErrInvalid
	}
	for _, label := range strings.Split(host, ".") {
		if !dataAPITargetDNSLabel.MatchString(label) {
			return target, managedpostgres.ErrInvalid
		}
	}
	return managedpostgres.DataAPIBindingTarget{Host: host, Port: p.cfg.Port, Database: p.cfg.Database, Login: login, TrustBundleSHA256: p.cfg.DataAPIInspectionTrustSHA256}, nil
}

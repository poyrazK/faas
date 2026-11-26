package firecracker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
)

func targetTestRequest() managedpostgres.CredentialRequest {
	binding := uuid.NewString()
	sum := sha256.Sum256([]byte(binding + "\x001"))
	return managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, BindingID: binding, IdentityKey: "gregale-binding-" + hex.EncodeToString(sum[:20]), Access: managedpostgres.CredentialDataAPI}
}

func TestDataAPIBindingTargetMatchesIssuedNativeCredential(t *testing.T) {
	p, scheduler, _ := newTestProvider(t)
	p.cfg.DataAPI = true
	p.cfg.DataAPIInspectionTrustSHA256 = strings.Repeat("a", 64)
	scheduler.vms[testDatabaseID] = &scheddpb.DatabaseRuntime{DatabaseId: testDatabaseID}
	req := targetTestRequest()
	material, err := p.IssueCredentials(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	target, err := p.DataAPIBindingTarget(t.Context(), req, material.ProviderIdentityID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if target.Login != material.Username || target.Database != material.Database || len(material.Endpoints) < 2 || target.Host != material.Endpoints[1].Host || target.Port != material.Endpoints[1].Port || material.Endpoints[1].Role != managedpostgres.EndpointPooled || target.TrustBundleSHA256 != p.cfg.DataAPIInspectionTrustSHA256 {
		t.Fatalf("provider expectation did not match issued credential")
	}
	// The public connection URL uses the same pooled endpoint; its password
	// is never returned by the target operation.
	uri, err := credentialdelivery.ConnectionURL(req.Access, material)
	if err != nil || !strings.Contains(uri, target.Host) {
		t.Fatal("issued URL did not select the expected pooled endpoint")
	}
	encoded, err := json.Marshal(target)
	if err != nil || string(encoded) != "{}" {
		t.Fatalf("private target was serialized: %s %v", encoded, err)
	}
	if _, err := p.DataAPIBindingTarget(t.Context(), req, material.ProviderIdentityID, 2); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Fatalf("accepted another credential generation: %v", err)
	}
}

func TestDataAPIBindingTargetRefusesUnsupportedOrConflictingEvidence(t *testing.T) {
	valid := targetTestRequest()
	for name, tc := range map[string]struct {
		change func(*Provider, *managedpostgres.CredentialRequest, *string)
		want   error
	}{
		"tls_require": {func(p *Provider, _ *managedpostgres.CredentialRequest, _ *string) { p.cfg.TLSMode = "require" }, managedpostgres.ErrUnsupported},
		"disabled":    {func(p *Provider, _ *managedpostgres.CredentialRequest, _ *string) { p.cfg.DataAPI = false }, managedpostgres.ErrUnsupported},
		"wrong_role":  {func(_ *Provider, _ *managedpostgres.CredentialRequest, id *string) { *id = "other" }, managedpostgres.ErrConflict},
		"wrong_access": {func(_ *Provider, r *managedpostgres.CredentialRequest, _ *string) {
			r.Access = managedpostgres.CredentialReadOnly
		}, managedpostgres.ErrInvalid},
		"bad_resource": {func(_ *Provider, r *managedpostgres.CredentialRequest, _ *string) {
			r.ProviderResourceID = "not-a-uuid"
		}, managedpostgres.ErrInvalid},
		"bad_binding": {func(_ *Provider, r *managedpostgres.CredentialRequest, _ *string) { r.BindingID = "" }, managedpostgres.ErrInvalid},
		"bad_generation_identity": {func(_ *Provider, r *managedpostgres.CredentialRequest, _ *string) {
			r.IdentityKey = "gregale-binding-" + strings.Repeat("A", 40)
		}, managedpostgres.ErrInvalid},
		"bad_host": {func(p *Provider, _ *managedpostgres.CredentialRequest, _ *string) { p.cfg.HostSuffix = "bad host" }, managedpostgres.ErrInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, _ := newTestProvider(t)
			p.cfg.DataAPI = true
			req := valid
			id := credentialRoleName(req.ProviderResourceID, req.IdentityKey, req.Access)
			tc.change(p, &req, &id)
			if _, err := p.DataAPIBindingTarget(t.Context(), req, id, 1); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDataAPIInspectionTrustConfiguration(t *testing.T) {
	backend := managedpostgres.BackendConfig{Region: "eu-central", Settings: map[string]string{settingHostSuffix: "db.gregale.test"}, DataAPIInspectionTrustSHA256: strings.Repeat("a", 64)}
	config, err := ParseConfig(backend)
	if err != nil || config.DataAPIInspectionTrustSHA256 != backend.DataAPIInspectionTrustSHA256 {
		t.Fatalf("valid trust configuration: %+v %v", config, err)
	}
	for _, invalid := range []string{strings.Repeat("A", 64), strings.Repeat("x", 64), strings.Repeat("a", 63)} {
		backend.DataAPIInspectionTrustSHA256 = invalid
		if _, err := ParseConfig(backend); err == nil {
			t.Fatalf("accepted invalid trust configuration %q", invalid)
		}
	}
}

package state

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Operator attestation of the exact target endpoint. No URI, password, options,
// source connection or provider-supplied credential material is accepted.
type ProjectEnvironmentCloneCheckpointPgEndpointObservation struct {
	RequestID, CredentialPlanSHA256, CredentialCiphertextSHA256                       string
	EndpointID, OwnerID, NativeResourceID, DataResourceID, ProviderIdentityID         string
	Host, Database                                                                    string
	Port                                                                              uint16
	PrivateScope, TLSVerified, IdentityDisabled, NoSourceAuthority, SQLScopeUnchanged bool
	AssignedAt, ValidThrough                                                          time.Time
}
type ProjectEnvironmentCloneCheckpointPgConnectionPlan struct {
	Credential                                              ProjectEnvironmentCloneCheckpointPostgresCredentialPlan
	CredentialObservationSHA256, CredentialCiphertextSHA256 string
	Endpoint                                                ProjectEnvironmentCloneCheckpointPgEndpointObservation
}
type ProjectEnvironmentCloneCheckpointPgConnectionValue struct {
	Plan                           ProjectEnvironmentCloneCheckpointPgConnectionPlan
	State, KeyID, CiphertextSHA256 string
	Ciphertext                     []byte
}
type ProjectEnvironmentCloneCheckpointPgConnectionProof struct {
	Prerequisites ProjectEnvironmentCloneCheckpointPostgresCredentialProof
	Credential    ProjectEnvironmentCloneCheckpointPostgresCredentialObservation
	Endpoint      ProjectEnvironmentCloneCheckpointPgEndpointObservation
}
type ProjectEnvironmentCloneCheckpointPgConnectionStore interface {
	ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgConnectionValue, error)
	SealProjectEnvironmentCloneCheckpointPgConnectionValue(context.Context, ProjectEnvironmentCloneLease, string, string, []byte, ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgConnectionValue, error)
}

func ProjectEnvironmentCloneCheckpointPgEndpointRequestID(p ProjectEnvironmentCloneCheckpointPostgresCredentialPlan) string {
	return "connection-" + ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(p)
}
func ProjectEnvironmentCloneCheckpointPgConnectionPlanSHA256(p ProjectEnvironmentCloneCheckpointPgConnectionPlan) string {
	return cloneCheckpointSHA(p)
}
func checkpointPgEndpointHostValid(host string) bool {
	if host == "" || len(host) > api.PostgresCheckpointConnectionHostMaxBytes || strings.ToLower(host) != host {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == "" && ip.String() == host
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > api.PostgresCheckpointConnectionHostLabelMaxBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}
func checkpointPgEndpointDatabaseValid(name string) bool {
	if name == "" || len(name) > api.PostgresCheckpointConnectionDatabaseMaxBytes {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
func validateCheckpointPgEndpoint(c ProjectEnvironmentCloneCheckpointPostgresCredential, o ProjectEnvironmentCloneCheckpointPgEndpointObservation, until time.Time) error {
	p := c.Plan
	native := p.Runtime.Verification.Isolation
	if c.State != "prepared" || o.RequestID != ProjectEnvironmentCloneCheckpointPgEndpointRequestID(p) || o.CredentialPlanSHA256 != cloneCheckpointSHA(p) || o.CredentialCiphertextSHA256 != c.CiphertextSHA256 || !validCloneProviderIdentity(o.EndpointID) || o.OwnerID != native.OwnerID || o.NativeResourceID != native.NativeResourceID || o.DataResourceID != p.DataResourceID || o.ProviderIdentityID != p.ProviderIdentityID || !checkpointPgEndpointHostValid(o.Host) || !checkpointPgEndpointDatabaseValid(o.Database) || o.Port == 0 || !o.PrivateScope || !o.TLSVerified || !o.IdentityDisabled || !o.NoSourceAuthority || !o.SQLScopeUnchanged || o.AssignedAt.Location() != time.UTC || o.AssignedAt.Nanosecond()%1000 != 0 || o.AssignedAt.Before(native.TargetCreatedAt) || o.AssignedAt.After(time.Now()) || o.ValidThrough.Before(until) {
		return ErrConflict
	}
	return nil
}

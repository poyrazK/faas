// Package toolgrant validates immutable, expiring delegation metadata. A grant
// neither starts an invocation nor conveys an application credential.
package toolgrant

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func ValidID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

// Existing platform resource identities include the legacy compact UUID form.
// New grant/request identities always use ValidID's canonical form.
func ValidResourceID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && (u.String() == id || strings.ReplaceAll(u.String(), "-", "") == id)
}

func ValidName(name string) bool {
	return len(name) > 0 && len(name) <= 128 && utf8.ValidString(name) && !strings.ContainsAny(name, "\x00\r\n")
}

func ValidateIntent(in api.ToolGrantIntent) error {
	if in.CredentialConnectionID != "" && (!in.AllowInvocation || !ValidID(in.CredentialConnectionID) || in.CredentialSecretRef != "") {
		return errors.New("invalid execution connection reference")
	}
	if in.CredentialSecretRef != "" && (!in.AllowInvocation || !ValidCredentialRef(in.CredentialSecretRef)) {
		return errors.New("invalid private credential reference")
	}
	if !ValidResourceID(in.KeyID) || !toolpublication.ValidID(in.Version) || !ValidName(in.Name) || in.ExpiresAt.IsZero() || in.ExpiresAt.Year() > 9999 {
		return errors.New("invalid exact tool grant")
	}
	return nil
}

func ValidateRecord(r api.ToolGrantRecord) error {
	if r.Credential != nil {
		raw, _ := json.Marshal(r.Credential.RequiredScopes)
		if len(raw) > api.ToolGrantIntentMaxBytes || len(r.Credential.RequiredScopes) > api.ToolPublicationMaxTools {
			return errors.New("private credential scope metadata exceeds review bounds")
		}
	}
	if (r.Credential != nil) != (r.Intent.CredentialSecretRef != "" || r.Intent.CredentialConnectionID != "") || r.Credential != nil && (r.Credential.ConnectionID != r.Intent.CredentialConnectionID || r.Credential.Scope != api.DefaultEnvScope || !toolpublication.ValidID(r.Credential.SourceSHA256) || r.Credential.SHA256 != CredentialDigest(r)) {
		return errors.New("invalid private credential binding")
	}
	if !ValidID(r.ID) || !ValidResourceID(r.AccountID) || !ValidResourceID(r.AppID) || !api.ValidAppSlug(r.App) ||
		(r.ProjectID != "" && !ValidResourceID(r.ProjectID)) || ValidateIntent(r.Intent) != nil || r.CreatedAt.IsZero() ||
		!r.Intent.ExpiresAt.After(r.CreatedAt) || r.Intent.ExpiresAt.Sub(r.CreatedAt) > api.ToolGrantMaxLifetime ||
		(r.RevokedAt != nil && r.RevokedAt.Before(r.CreatedAt)) {
		return errors.New("invalid tool grant binding")
	}
	return nil
}

func Active(r api.ToolGrantRecord, now time.Time) bool {
	return ValidateRecord(r) == nil && r.RevokedAt == nil && now.Before(r.Intent.ExpiresAt)
}

func SameIntent(a, b api.ToolGrantIntent) bool {
	return a.KeyID == b.KeyID && a.Version == b.Version && a.Name == b.Name && a.AllowInvocation == b.AllowInvocation && a.CredentialSecretRef == b.CredentialSecretRef && a.CredentialConnectionID == b.CredentialConnectionID && a.ExpiresAt.Equal(b.ExpiresAt)
}

func ValidCredentialRef(ref string) bool {
	key := strings.TrimPrefix(ref, api.SecretRefPrefix)
	return strings.HasPrefix(ref, api.SecretRefPrefix) && strings.HasPrefix(key, "MCP_") && api.ValidateEnvKey(key) == nil
}

func CredentialDigest(r api.ToolGrantRecord) string {
	if r.Credential == nil {
		return ""
	}
	c := *r.Credential
	c.SHA256 = ""
	raw, _ := json.Marshal(struct {
		ID, Account, App, Version, Tool, Key, Ref string
		Credential                                api.ToolGrantCredential
	}{r.ID, r.AccountID, r.AppID, r.Intent.Version, r.Intent.Name, r.Intent.KeyID, r.Intent.CredentialSecretRef, c})
	return toolpublication.Digest(raw)
}

func CredentialMatches(g api.ToolGrantRecord, mode, binding string) bool {
	if g.Credential == nil {
		return mode == "open" && binding == ""
	}
	return mode == "external-oauth" && binding == g.Credential.SHA256 && toolpublication.ValidID(binding)
}

func SameCredential(a, b *api.ToolGrantCredential) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.SHA256 == b.SHA256 && a.SourceSHA256 == b.SourceSHA256 && a.Scope == b.Scope && a.ConnectionID == b.ConnectionID && slices.Equal(a.RequiredScopes, b.RequiredScopes)
}

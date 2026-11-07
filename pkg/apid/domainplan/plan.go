// Package domainplan normalizes declarative domain intent and fingerprints
// captured bindings. It performs no DNS, certificate, or network operations.
package domainplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func Normalize(in api.DomainManifest) (api.DomainManifest, error) {
	if in.Version != 1 || len(in.Domains) == 0 || len(in.Domains) > api.DomainPlanMaxBindings {
		return in, fmt.Errorf("version must be 1 and domains must contain 1..%d bindings", api.DomainPlanMaxBindings)
	}
	in.Domains = append([]api.DomainBindingSpec(nil), in.Domains...)
	seen := make(map[string]bool)
	for i := range in.Domains {
		d := &in.Domains[i]
		d.Domain = strings.ToLower(strings.TrimSpace(d.Domain))
		d.App, d.Environment = strings.TrimSpace(d.App), strings.TrimSpace(d.Environment)
		if d.Domain == "" || d.App == "" || seen[d.Domain] || d.Environment != "" && !api.ValidProjectEnvironmentSlug(d.Environment) {
			return in, fmt.Errorf("each binding needs a unique domain, app, and valid optional environment")
		}
		seen[d.Domain] = true
	}
	sort.Slice(in.Domains, func(i, j int) bool { return in.Domains[i].Domain < in.Domains[j].Domain })
	return in, nil
}

// Fingerprint binds the public diff to private identities as well: a deleted
// and recreated claim, an intervening move, or a changed serving deployment
// must invalidate the reviewed plan without exposing ownership tokens.
func Fingerprint(plan api.DomainPlan, capture any) (string, error) {
	plan.SHA256 = ""
	b, err := json.Marshal(struct {
		Plan    api.DomainPlan
		Capture any
	}{plan, capture})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func ValidDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}

func Rollback(plan api.DomainPlan) *api.DomainManifest {
	m := api.DomainManifest{Version: 1}
	for _, c := range plan.Changes {
		if c.Action == "move" && c.Before != nil {
			m.Domains = append(m.Domains, api.DomainBindingSpec{Domain: c.Domain, App: c.Before.App, Environment: c.Before.Environment})
		}
	}
	if len(m.Domains) == 0 {
		return nil
	}
	return &m
}

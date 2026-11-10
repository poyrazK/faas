package main

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeRuleHeaderSecretCacheMax bounds the opened header values kept in
// memory; past it the cache starts over.
const edgeRuleHeaderSecretCacheMax = 10_000

var errEdgeRuleHeaderSecretNamespace = errors.New("sealed edge rule header value has the wrong namespace")

// edgeRuleHeaderSecretReader is the ADR-20261108110000 read the host loader
// needs.
type edgeRuleHeaderSecretReader interface {
	EdgeRuleHeaderSecretsByID(ctx context.Context, appID string, ids []string) ([]state.EdgeRuleHeaderSecret, error)
}

// edgeRuleHeaderSecretOpener opens one sealed header value.
type edgeRuleHeaderSecretOpener func(sealed []byte) (string, error)

// newEdgeRuleHeaderSecretOpener opens values sealed to the edge domain (or,
// before the domain, the fleet and host identities), under the header
// namespace only.
func newEdgeRuleHeaderSecretOpener(identities []*age.X25519Identity) edgeRuleHeaderSecretOpener {
	if len(identities) == 0 {
		return nil
	}
	return func(sealed []byte) (string, error) {
		namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
		if err != nil {
			return "", err
		}
		if namespace != api.EdgeRuleHeaderSecretSealNamespace {
			return "", errEdgeRuleHeaderSecretNamespace
		}
		return string(plaintext), nil
	}
}

// edgeRuleHeaderSecrets caches opened values by row id. A row never changes,
// so an entry never goes stale; the edge re-seal keeps the id and value.
type edgeRuleHeaderSecrets struct {
	mu     sync.Mutex
	values map[string]string
}

// Entries are keyed by app and id, so a rule never reads another app's
// value from the cache either.
func (c *edgeRuleHeaderSecrets) get(appID, id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.values[appID+"/"+id]
	return v, ok
}

func (c *edgeRuleHeaderSecrets) put(appID, id, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil || len(c.values) >= edgeRuleHeaderSecretCacheMax {
		c.values = map[string]string{}
	}
	c.values[appID+"/"+id] = value
}

// resolveEdgeRuleHeaderSecrets puts each sealed request header value back
// into a copy of its rule before compilation (ADR-20261108110000). Only rows
// of the rule's own app are read. A rule whose value is missing or does not
// open is disabled for this load and logged, so it is never applied without
// its credential. A store error fails the load, keeping the host's
// last-known-good rules.
func (g *gatewaydEdgeRules) resolveEdgeRuleHeaderSecrets(ctx context.Context, rules []state.EdgeRule) ([]state.EdgeRule, error) {
	wanted := map[string][]string{} // app id -> uncached ids
	found := false
	for _, r := range rules {
		for _, id := range edgeRuleHeaderSecretIDs(r) {
			found = true
			if _, ok := g.headerSecrets.get(r.AppID, id); !ok {
				wanted[r.AppID] = append(wanted[r.AppID], id)
			}
		}
	}
	if !found {
		return rules, nil
	}
	if len(wanted) > 0 {
		reader, ok := g.store.(edgeRuleHeaderSecretReader)
		if ok && g.headerSecretOpener != nil {
			apps := make([]string, 0, len(wanted))
			for appID := range wanted {
				apps = append(apps, appID)
			}
			sort.Strings(apps)
			for _, appID := range apps {
				rows, err := reader.EdgeRuleHeaderSecretsByID(ctx, appID, wanted[appID])
				if err != nil {
					return nil, err
				}
				for _, row := range rows {
					value, err := g.headerSecretOpener(row.Ciphertext)
					if err != nil {
						g.secretLog().Warn("sealed edge rule header value does not open; its rule is not applied", "secret", row.ID, "app", appID, "err", err)
						continue
					}
					g.headerSecrets.put(row.AppID, row.ID, value)
				}
			}
		}
	}
	out := make([]state.EdgeRule, len(rules))
	copy(out, rules)
	for i := range out {
		if len(edgeRuleHeaderSecretIDs(out[i])) == 0 {
			continue
		}
		headers := *out[i].Action.Headers
		ops := append([]state.EdgeRuleHeaderOp(nil), headers.RequestHeaders...)
		for j := range ops {
			if ops[j].SecretID == "" {
				continue
			}
			value, ok := g.headerSecrets.get(out[i].AppID, ops[j].SecretID)
			if !ok {
				g.secretLog().Warn("sealed edge rule header value is unavailable; the rule is not applied", "rule", out[i].ID, "secret", ops[j].SecretID)
				out[i].Enabled = false
				break
			}
			ops[j].Value = value
		}
		headers.RequestHeaders = ops
		out[i].Action.Headers = &headers
	}
	return out, nil
}

// edgeRuleHeaderSecretIDs lists the sealed values a headers rule references.
func edgeRuleHeaderSecretIDs(r state.EdgeRule) []string {
	if r.Kind != state.EdgeRuleKindHeaders || r.Action.Headers == nil {
		return nil
	}
	var ids []string
	for _, op := range r.Action.Headers.RequestHeaders {
		if op.SecretID != "" {
			ids = append(ids, op.SecretID)
		}
	}
	return ids
}

func (g *gatewaydEdgeRules) secretLog() *slog.Logger {
	if g.log != nil {
		return g.log
	}
	return slog.Default()
}

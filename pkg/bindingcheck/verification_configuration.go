package bindingcheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"sort"
)

func VerificationConfiguration(i api.AppBindingInventory, p api.BindingReleasePolicy) string {
	var items []api.AppBindingInventoryItem
	for _, item := range i.Bindings {
		if !verificationItemInScope(item, p.Scope) {
			continue
		}
		items = append(items, api.AppBindingInventoryItem{Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, Access: item.Access, State: item.State,
			ServiceTargetDeploymentID: item.ServiceTargetDeploymentID, Transport: item.Transport, CredentialGeneration: item.CredentialGeneration, RotationPending: item.RotationPending,
			QueueCanarySupported: item.QueueCanarySupported, QueueEnvironment: item.QueueEnvironment, CredentialConfigured: item.CredentialConfigured, OutboundProbe: item.OutboundProbe,
			HTTPURL: item.HTTPURL, HTTPSURL: item.HTTPSURL, HTTPSEnv: item.HTTPSEnv,
			AllowedMethods: sortedVerificationStrings(item.AllowedMethods), AllowedPathPrefixes: sortedVerificationStrings(item.AllowedPathPrefixes)})
	}
	sort.Slice(items, func(a, b int) bool {
		return items[a].Type+"\x00"+items[a].Binding+"\x00"+items[a].Name+"\x00"+items[a].Scope < items[b].Type+"\x00"+items[b].Binding+"\x00"+items[b].Name+"\x00"+items[b].Scope
	})
	encoded, _ := json.Marshal(items) // Only strings, integers and booleans; cannot fail.
	return string(encoded)
}

func verificationItemInScope(item api.AppBindingInventoryItem, scope string) bool {
	switch item.Type {
	case api.BindingTypePostgres, api.BindingTypeObjectStorage:
		return item.Scope == scope && (item.Type != api.BindingTypePostgres || item.Access != "migration")
	case api.BindingTypeQueue:
		return api.QueueBindingInScope(item, scope)
	default:
		return true
	}
}

func sortedVerificationStrings(values []string) []string {
	copy := slices.Clone(values)
	sort.Strings(copy)
	return copy
}

func VerificationReviewDigest(i api.AppBindingInventory, p api.BindingReleasePolicy) string {
	if i.RuntimeFreshness == nil || i.RuntimeFreshness.ConfigChangedAt == nil {
		return ""
	}
	body, _ := json.Marshal(struct {
		Deployment, Scope, Configuration string
		Policy                           api.BindingReleasePolicy
		ConfigChangedAt                  any
	}{i.VerificationDeploymentID, i.VerificationScope, VerificationConfiguration(i, p), p, i.RuntimeFreshness.ConfigChangedAt})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

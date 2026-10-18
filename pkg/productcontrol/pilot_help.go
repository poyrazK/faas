package productcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func pilotDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && hex.EncodeToString(data) == value
}

func validatePilotHelp(m api.ProductPilotManifest, names, tenants map[string]bool) error {
	if len(m.HelpCases) > api.ProductControlPilotMaxCases {
		return fmt.Errorf("pilot help cases exceed their bound")
	}
	ids := map[string]bool{}
	for _, c := range m.HelpCases {
		req := api.ProductHelpCreate{ID: c.RequestID, Kind: c.Kind, Feature: c.Feature, OperationID: c.OperationID, Message: "pilot expectation"}
		if !slug.MatchString(c.Name) || names[c.Name] || !tenants[c.TenantID] || ids[c.RequestID] || ValidateHelpCreate(req) != nil || !HelpStatus(c.ExpectedStatus) || c.ExpectedVersion < 1 || c.ExpectedVersion > api.ProductControlHelpMaxUpdates || !pilotDigest(c.MessageSHA256) || !pilotDigest(c.ResponseSHA256) || !pilotDigest(c.ContextSHA256) {
			return fmt.Errorf("help case %q requires unique scoped references, status/version and reviewed hashes", c.Name)
		}
		if c.Kind == "feature" && c.Feature != m.Feature {
			return fmt.Errorf("help case %q must reference the pilot feature", c.Name)
		}
		if c.Kind == "run" && !pilotHasOperation(m, c.TenantID, c.OperationID) {
			return fmt.Errorf("help case %q must reference a selected run of the same customer", c.Name)
		}
		names[c.Name], ids[c.RequestID] = true, true
	}
	return nil
}

func pilotHasOperation(m api.ProductPilotManifest, tenant, operation string) bool {
	for _, c := range m.Cases {
		if c.TenantID == tenant && c.OperationID == operation {
			return true
		}
	}
	return false
}

// PilotHelpHashes hashes exact UTF-8 text and canonical JSON of the typed context.
// Callers must establish the request's app/customer scope before reporting hashes.
func PilotHelpHashes(r api.ProductHelpRequest) (message, response, context string, err error) {
	msg, reply := sha256.Sum256([]byte(r.Request.Message)), sha256.Sum256([]byte(r.Response))
	raw, err := json.Marshal(r.Context)
	if err != nil {
		return "", "", "", err
	}
	raw, err = operations.CanonicalJSON(raw)
	if err != nil {
		return "", "", "", err
	}
	captured := sha256.Sum256(raw)
	return hex.EncodeToString(msg[:]), hex.EncodeToString(reply[:]), hex.EncodeToString(captured[:]), nil
}

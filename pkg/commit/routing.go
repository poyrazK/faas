// adr: 487
package commit

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// NormalizeRouting validates the typed lane key and normalizes UUID identity.
// PostgreSQL JSONB compares the retained routing independently of event data.
func NormalizeRouting(routing *api.CommitRouting) (json.RawMessage, error) {
	if routing == nil {
		return nil, nil
	}
	if routing.Version != 2 {
		return nil, errors.New("commit: routing version must be 2")
	}
	if _, err := workpolicy.CanonicalScalar(routing.Key); err != nil {
		return nil, errors.New("commit: routing key must be a bounded nonempty JSON string, number, or boolean")
	}
	normalized := *routing
	if normalized.PlatformTenantID != "" {
		id, err := uuid.Parse(normalized.PlatformTenantID)
		if err != nil {
			return nil, errors.New("commit: routing customer must be a UUID")
		}
		normalized.PlatformTenantID = id.String()
	}
	return json.Marshal(normalized)
}

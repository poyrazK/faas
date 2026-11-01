package dataapiusage

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
)

// keyServerKeys rewrites the server key hashes of reported access settings
// as keyed digests under the data-api ring once its keyed writes are on, so
// a database writer cannot add a server key of their own (ADR
// 20261029202403 amendment 1). realtimed asks apid to match them. A hash
// already keyed is kept as it is.
func keyServerKeys(access []byte) ([]byte, error) {
	policy := credhash.For(credhash.FamilyDataAPI)
	if len(access) == 0 || policy == nil || !policy.KeyedWrites {
		return access, nil
	}
	var config api.DataAPIAccessConfig
	if err := json.Unmarshal(access, &config); err != nil {
		return nil, fmt.Errorf("dataapiusage: decode access settings: %w", err)
	}
	for i, key := range config.ServerKeys {
		sum, err := hex.DecodeString(key.SHA256)
		if err != nil || len(sum) != credhash.PlainSize {
			continue
		}
		config.ServerKeys[i].SHA256 = hex.EncodeToString(policy.Store(api.DataAPIServerKeyCredentialTag, sum))
	}
	out, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("dataapiusage: encode access settings: %w", err)
	}
	return out, nil
}

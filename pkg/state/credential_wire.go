package state

import "encoding/json"

// apiKeyWire is an APIKey as apid sends it to a gateway (ADR
// 20261029202403). APIKey keeps its hosting and Runs identities out of
// JSON so no API response carries them; the gateway's middleware still
// needs them, so the wire names them explicitly.
type apiKeyWire struct {
	APIKey
	WireHostingSiteID      string   `json:"hosting_site_id,omitempty"`
	WireHostingOIDCTrustID string   `json:"hosting_oidc_trust_id,omitempty"`
	WireHostingOperations  []string `json:"hosting_operations,omitempty"`
	WireRunsPrincipalID    string   `json:"runs_principal_id,omitempty"`
}

// MarshalAPIKeyWire encodes an authenticated key for a gateway.
func MarshalAPIKeyWire(k APIKey) ([]byte, error) {
	return json.Marshal(apiKeyWire{APIKey: k, WireHostingSiteID: k.HostingSiteID, WireHostingOIDCTrustID: k.HostingOIDCTrustID,
		WireHostingOperations: k.HostingOperations, WireRunsPrincipalID: k.RunsPrincipalID})
}

// UnmarshalAPIKeyWire decodes MarshalAPIKeyWire's encoding.
func UnmarshalAPIKeyWire(data []byte) (APIKey, error) {
	var w apiKeyWire
	if err := json.Unmarshal(data, &w); err != nil {
		return APIKey{}, err
	}
	k := w.APIKey
	k.HostingSiteID, k.HostingOIDCTrustID, k.HostingOperations, k.RunsPrincipalID = w.WireHostingSiteID, w.WireHostingOIDCTrustID, w.WireHostingOperations, w.WireRunsPrincipalID
	return k, nil
}

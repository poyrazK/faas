package api

// FeatureFlagKillResponse is the result of KillProjectFlag. Changed is false
// when the flag was already killed.
type FeatureFlagKillResponse struct {
	Key     string `json:"key"`
	Killed  bool   `json:"killed"`
	Changed bool   `json:"changed"`
	Version int64  `json:"version"`
}

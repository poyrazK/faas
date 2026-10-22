package api

import "fmt"

// EnvironmentRecoveryRetention is explicit artifact storage intent. Removing it
// releases policy protection; it never conveys trust or execution authority.
type EnvironmentRecoveryRetention struct {
	Releases int   `json:"releases"`
	Seconds  int   `json:"seconds"`
	MaxBytes int64 `json:"max_bytes"`
}

func (p EnvironmentRecoveryRetention) Validate() error {
	if p.Releases < 1 || p.Releases > EnvironmentRecoveryMaxReleases || p.Seconds < 1 || p.Seconds > EnvironmentRecoveryMaxSeconds || p.MaxBytes < 1 || p.MaxBytes > EnvironmentRecoveryMaxBytes {
		return fmt.Errorf("recovery_retention requires releases 1..%d, seconds 1..%d and max_bytes 1..%d", EnvironmentRecoveryMaxReleases, EnvironmentRecoveryMaxSeconds, EnvironmentRecoveryMaxBytes)
	}
	return nil
}

package api

import "time"

// BindingApplicationAdoption reports version-fenced self-attestations for
// authorized resident workloads. Counts refer to workload/secret pairs.
// It contains no credential values, fingerprints or private binding IDs.
type BindingApplicationAdoption struct {
	Source          string                        `json:"source"`
	Status          string                        `json:"status"`
	ObservedAt      time.Time                     `json:"observed_at"`
	Complete        bool                          `json:"complete"`
	SecretsExpected int                           `json:"secrets_expected"`
	SecretsObserved int                           `json:"secrets_observed"`
	Reload          BindingAdoptionCounts         `json:"reload"`
	Application     BindingAdoptionCounts         `json:"application"`
	Targets         []BindingApplicationAckTarget `json:"targets"`
}

type BindingAdoptionCounts struct {
	Current int `json:"current"`
	Failed  int `json:"failed"`
	Stale   int `json:"stale"`
	Unknown int `json:"unknown"`
}

// Application acknowledgements remain separate from guest projection/signal
// outcomes. An older projection does not erase a newer application receipt.
type BindingApplicationAckTarget struct {
	DeploymentID             string     `json:"deployment_id"`
	InstanceID               string     `json:"instance_id"`
	WorkloadName             string     `json:"workload_name,omitempty"`
	RuntimeState             string     `json:"runtime_state"`
	Key                      string     `json:"key"`
	ReloadSupport            string     `json:"reload_support"`
	CurrentVersion           int64      `json:"current_version"`
	ReloadVersion            int64      `json:"reload_version"`
	Projection               string     `json:"projection,omitempty"`
	Signal                   string     `json:"signal,omitempty"`
	ReloadAt                 *time.Time `json:"reload_at,omitempty"`
	ApplicationAckVersion    int64      `json:"application_ack_version"`
	ApplicationAck           string     `json:"application_ack,omitempty"`
	ApplicationAckAt         *time.Time `json:"application_ack_at,omitempty"`
	ProcessGeneration        string     `json:"process_generation,omitempty"`
	ApplicationAckGeneration string     `json:"application_ack_generation,omitempty"`
	ReloadStatus             string     `json:"reload_status,omitempty"`
	ReloadReason             string     `json:"reload_reason,omitempty"`
	ApplicationAckStatus     string     `json:"application_ack_status,omitempty"`
	ApplicationAckReason     string     `json:"application_ack_reason,omitempty"`
}

func BindingCredentialSecretKeys(kind, binding string) []string {
	switch kind {
	case BindingTypePostgres:
		return []string{binding}
	case BindingTypeObjectStorage:
		keys := []string{}
		for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
			keys = append(keys, binding+suffix)
		}
		return keys
	default:
		return nil
	}
}

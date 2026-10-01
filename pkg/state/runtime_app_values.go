package state

import (
	"context"
	"encoding/json"
)

// RuntimeAppSecretGrants is the deployment's secret selection intent read in
// the same snapshot as its environment owner and runtime values.
type RuntimeAppSecretGrants struct {
	OverrideEnvSecrets   json.RawMessage
	Sidecars             json.RawMessage
	ReloadSignal         string
	SidecarReloadSignals map[string]string
}

// RuntimeAppValuesSnapshot contains plaintext configuration and sealed secret
// inputs for one deployment. Delivery observations are not configuration.
type RuntimeAppValuesSnapshot struct {
	RuntimeAppEnvSnapshot
	Secrets      []AppSecret
	SecretGrants RuntimeAppSecretGrants
}

type RuntimeAppValuesStore interface {
	RuntimeAppValuesForDeployment(context.Context, string, string, string) (RuntimeAppValuesSnapshot, error)
}

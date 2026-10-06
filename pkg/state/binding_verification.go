package state

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// BindingVerificationPin is server-selected metadata atomically admitted with
// a reserved platform canary. Revision hashes binding configuration, private
// credential revision facts and the runtime configuration change stamp.
type BindingVerificationPin struct {
	TargetDeploymentID   string                        `json:"target_deployment_id,omitempty"`
	Type                 string                        `json:"type"`
	Binding              string                        `json:"binding"`
	Revision             string                        `json:"revision"`
	CredentialGeneration *int64                        `json:"credential_generation,omitempty"`
	OutboundProbe        *api.OutboundBindingProbeSpec `json:"outbound_probe,omitempty"`
}

type BindingVerificationTask struct {
	Pin                                 BindingVerificationPin
	DeploymentID, Scope, Status, Stdout string
	CreatedAt                           time.Time
	FinishedAt                          *time.Time
	OutputTruncated                     bool
	ExitCode                            *int
}

type BindingVerificationStore interface {
	ListBindingVerificationTasks(context.Context, string, string, []string, ...BindingVerificationSelection) ([]BindingVerificationTask, error)
}

// BindingVerificationSelection prioritizes evidence for a deployment. Explicit
// selectors never fall back; default inventory may retain stale prior evidence
// when the selected deployment has no admitted probe for a binding.
type BindingVerificationSelection struct {
	DeploymentID  string
	AllowFallback bool
}

func resolveBindingVerificationSelection(selections []BindingVerificationSelection) (BindingVerificationSelection, error) {
	if len(selections) == 0 {
		return BindingVerificationSelection{}, nil
	}
	if len(selections) != 1 {
		return BindingVerificationSelection{}, fmt.Errorf("state: expected one binding verification selection")
	}
	selection := selections[0]
	parsed, err := uuid.Parse(selection.DeploymentID)
	if err != nil {
		return BindingVerificationSelection{}, fmt.Errorf("state: invalid binding verification deployment: %w", err)
	}
	selection.DeploymentID = parsed.String()
	return selection, nil
}

var bindingRevisionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateBindingVerificationPin(params CreateAppTaskParams) error {
	pin := params.BindingVerification
	if pin == nil {
		if params.RequireLiveDeployment && (params.Kind != AppTaskKindManual || params.ExclusiveOperationID != "" || params.FailureRules != nil || params.OccurrenceID != "" || params.StartDeadlineAt != nil || !api.IsServiceBindingSmokeCommand(params.Command, params.CommandShell)) {
			return fmt.Errorf("%w: explicit deployment selection requires a verification pin or a reserved service smoke command", ErrAppTaskInvalid)
		}
		return nil
	}
	command := api.AppTaskServiceBindingProbeCommand
	switch pin.Type {
	case api.BindingTypePostgres:
		command = api.AppTaskPostgresBindingProbeCommand
	case api.BindingTypeObjectStorage:
		command = api.AppTaskObjectStorageBindingProbeCommand
	case api.BindingTypeOutbound:
		command = api.AppTaskOutboundBindingProbeCommand
	}
	commandLen := 2
	if pin.TargetDeploymentID != "" {
		id, err := uuid.Parse(pin.TargetDeploymentID)
		if err != nil || id == uuid.Nil || id.String() != pin.TargetDeploymentID || pin.Type != api.BindingTypeService || len(params.Command) != 3 || params.Command[2] != pin.TargetDeploymentID {
			return fmt.Errorf("%w: invalid exact service verification target", ErrAppTaskInvalid)
		}
		commandLen = 3
	}
	if pin.Type == api.BindingTypeOutbound {
		commandLen = 3
		var spec api.OutboundBindingProbeSpec
		if pin.OutboundProbe == nil || !pin.OutboundProbe.Valid() || pin.OutboundProbe.IntegrationID != pin.Binding || len(params.Command) != 3 || json.Unmarshal([]byte(params.Command[2]), &spec) != nil || spec != *pin.OutboundProbe {
			return fmt.Errorf("%w: invalid outbound verification spec", ErrAppTaskInvalid)
		}
	} else if pin.OutboundProbe != nil {
		return fmt.Errorf("%w: unexpected outbound verification spec", ErrAppTaskInvalid)
	}
	if (pin.Type != api.BindingTypeService && pin.Type != api.BindingTypePostgres && pin.Type != api.BindingTypeObjectStorage && pin.Type != api.BindingTypeOutbound) ||
		pin.Binding == "" || !bindingRevisionPattern.MatchString(pin.Revision) ||
		params.Kind != AppTaskKindManual || params.ExclusiveOperationID != "" || params.FailureRules != nil || params.OccurrenceID != "" || params.StartDeadlineAt != nil || params.CommandShell || len(params.Command) != commandLen ||
		params.Command[0] != command || params.Command[1] != pin.Binding ||
		(pin.CredentialGeneration != nil && *pin.CredentialGeneration < 1) {
		return fmt.Errorf("%w: invalid binding verification pin", ErrAppTaskInvalid)
	}
	return nil
}

func cloneBindingVerificationPin(pin *BindingVerificationPin) *BindingVerificationPin {
	if pin == nil {
		return nil
	}
	copy := *pin
	if pin.OutboundProbe != nil {
		spec := *pin.OutboundProbe
		copy.OutboundProbe = &spec
	}
	if pin.CredentialGeneration != nil {
		value := *pin.CredentialGeneration
		copy.CredentialGeneration = &value
	}
	return &copy
}

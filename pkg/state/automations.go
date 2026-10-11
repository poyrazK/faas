package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Automation struct {
	AppID            string
	Name             string
	Version          int64
	Draft            json.RawMessage
	Published        json.RawMessage
	PublishedVersion int64
	Enabled          bool
	UpdatedAt        time.Time
}

// AutomationRevision is an immutable snapshot created whenever a dashboard
// automation is published. LegacySnapshot rows were seeded from the latest
// publication present when revision history was introduced.
type AutomationRevision struct {
	AppID                string
	Name                 string
	Version              int64
	Definition           json.RawMessage
	RecordedAt           time.Time
	LegacySnapshot       bool
	PublishedByAccountID string
	PublishedByAPIKeyID  string
}

type AutomationRevisionListOptions struct {
	Limit  int
	Offset int
}

const maxAutomationRevisionOffset = int(^uint32(0) >> 1)

func validAutomationRevisionListOptions(opts AutomationRevisionListOptions) bool {
	return opts.Limit > 0 && opts.Limit <= 100 && opts.Offset >= 0 && opts.Offset <= maxAutomationRevisionOffset
}

type AutomationMutation struct {
	Action           string
	ExpectedVersion  int64
	Draft            json.RawMessage
	Enabled          bool
	TakeOverManifest bool
	RestoreManifest  bool
	ActorAccountID   string
	ActorAPIKeyID    string
}

type AutomationStore interface {
	ListAutomations(context.Context, string) ([]Automation, error)
	ListAutomationRevisions(context.Context, string, string, AutomationRevisionListOptions) ([]AutomationRevision, int, error)
	GetAutomationRevision(context.Context, string, string, int64) (AutomationRevision, error)
	MutateAutomation(context.Context, string, string, AutomationMutation) (Automation, error)
	EffectiveWorkflowDefinitions(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

var (
	ErrAutomationVersionConflict   = errors.New("automation: version changed; reload before editing")
	ErrAutomationOwnershipConflict = errors.New("automation: explicit confirmation is required to change YAML ownership")
	ErrAutomationInvalid           = errors.New("automation: invalid definition")
	ErrAutomationRevisionNotFound  = errors.New("automation: revision not found")
)

type AutomationQuotaError struct {
	Plan            api.Plan
	Limit, Observed int
}

func (e *AutomationQuotaError) Error() string {
	return fmt.Sprintf("automation: definition quota exceeded (%d/%d)", e.Observed, e.Limit)
}

func copyAutomation(a Automation) Automation {
	a.Draft = cloneWorkflowJSON(a.Draft)
	a.Published = cloneWorkflowJSON(a.Published)
	return a
}

func copyAutomationRevision(revision AutomationRevision) AutomationRevision {
	revision.Definition = cloneWorkflowJSON(revision.Definition)
	return revision
}

// Dashboard publication owns one name, including while paused. Unpublished
// drafts do not change deployed YAML intent. Both paths emit WorkflowSpec.
func mergeAutomationDefinitions(manifest json.RawMessage, records []Automation) (json.RawMessage, error) {
	var definitions []api.WorkflowSpec
	if len(manifest) > 0 && string(manifest) != "null" {
		if err := json.Unmarshal(manifest, &definitions); err != nil {
			return nil, err
		}
	}
	byName := make(map[string]api.WorkflowSpec)
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	for _, record := range records {
		if len(record.Published) == 0 {
			continue
		}
		var definition api.WorkflowSpec
		if err := json.Unmarshal(record.Published, &definition); err != nil {
			return nil, err
		}
		if definition.Trigger != nil && definition.Trigger.Type != "manual" {
			enabled := record.Enabled
			definition.Trigger.Enabled = &enabled
		}
		byName[record.Name] = definition
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	definitions = make([]api.WorkflowSpec, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, byName[name])
	}
	return json.Marshal(definitions)
}

func mutateAutomation(appID, name string, mutation AutomationMutation, previous *Automation, manifest json.RawMessage, records []Automation, plan api.Plan, hasDeployment bool) (Automation, error) {
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\\x00") || len(name) > api.AutomationNameMaxBytes || mutation.ExpectedVersion < 0 {
		return Automation{}, ErrAutomationInvalid
	}
	current := Automation{AppID: appID, Name: name, Enabled: true}
	if previous != nil {
		current = copyAutomation(*previous)
	}
	if current.Version != mutation.ExpectedVersion {
		return Automation{}, ErrAutomationVersionConflict
	}
	var deployed []api.WorkflowSpec
	if len(manifest) > 0 {
		if err := json.Unmarshal(manifest, &deployed); err != nil {
			return Automation{}, err
		}
	}
	fromManifest := false
	names := map[string]bool{name: true}
	for _, definition := range deployed {
		names[definition.Name] = true
		if definition.Name == name {
			fromManifest = true
		}
	}
	for _, record := range records {
		names[record.Name] = true
	}
	if mutation.Action == "save" && len(names) > plan.WorkflowMaxPerApp() {
		return Automation{}, &AutomationQuotaError{plan, plan.WorkflowMaxPerApp(), len(names)}
	}
	switch mutation.Action {
	case "save":
		if len(mutation.Draft) == 0 || int64(len(mutation.Draft)) > api.AutomationDefinitionMaxBytes {
			return Automation{}, ErrAutomationInvalid
		}
		var definition api.WorkflowSpec
		if err := json.Unmarshal(mutation.Draft, &definition); err != nil || definition.Name != name {
			return Automation{}, ErrAutomationInvalid
		}
		if definition.Steps == nil {
			definition.Steps = []api.WorkflowStepSpec{}
		}
		current.Draft, _ = json.Marshal(definition)
	case "publish":
		if previous == nil {
			return Automation{}, ErrNotFound
		}
		if !hasDeployment {
			return Automation{}, ErrWorkflowEventTargetUnavailable
		}
		if len(names) > plan.WorkflowMaxPerApp() {
			return Automation{}, &AutomationQuotaError{plan, plan.WorkflowMaxPerApp(), len(names)}
		}
		var definition api.WorkflowSpec
		if err := json.Unmarshal(current.Draft, &definition); err != nil {
			return Automation{}, fmt.Errorf("%w: %w", ErrAutomationInvalid, err)
		}
		if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
			return Automation{}, fmt.Errorf("%w: %w", ErrAutomationInvalid, err)
		}
		if current.PublishedVersion == 0 && fromManifest && !mutation.TakeOverManifest {
			return Automation{}, ErrAutomationOwnershipConflict
		}
		if current.PublishedVersion == 0 {
			current.Enabled = definition.Trigger == nil || definition.Trigger.Enabled == nil || *definition.Trigger.Enabled
		}
		current.Published = cloneWorkflowJSON(current.Draft)
		current.PublishedVersion = current.Version + 1
	case "enable":
		if previous == nil || len(current.Published) == 0 {
			return Automation{}, ErrNotFound
		}
		if mutation.Enabled {
			if !plan.WorkflowsAllowed() {
				return Automation{}, api.ErrPlanWorkflowsNotAllowed(plan)
			}
			var definition api.WorkflowSpec
			if err := json.Unmarshal(current.Published, &definition); err != nil {
				return Automation{}, ErrAutomationInvalid
			}
			if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
				return Automation{}, fmt.Errorf("%w: %w", ErrAutomationInvalid, err)
			}
		}
		current.Enabled = mutation.Enabled
	case "delete":
		if previous == nil {
			return Automation{}, ErrNotFound
		}
		if current.PublishedVersion > 0 && fromManifest && !mutation.RestoreManifest {
			return Automation{}, ErrAutomationOwnershipConflict
		}
	default:
		return Automation{}, ErrInvalidArgument
	}
	current.Version++
	current.UpdatedAt = time.Now().UTC()
	return current, nil
}

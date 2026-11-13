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

type StoredWorkflow struct {
	TestSuite, TestReport json.RawMessage
	AppID                 string
	Name                  string
	Version               int64
	Draft                 json.RawMessage
	Published             json.RawMessage
	PublishedVersion      int64
	Enabled               bool
	UpdatedAt             time.Time
}

// StoredWorkflowRevision is an immutable snapshot created whenever a dashboard
// stored workflow is published. LegacySnapshot rows were seeded from the latest
// publication present when revision history was introduced.
type StoredWorkflowRevision struct {
	CheckEvidence        json.RawMessage
	AppID                string
	Name                 string
	Version              int64
	Definition           json.RawMessage
	RecordedAt           time.Time
	LegacySnapshot       bool
	PublishedByAccountID string
	PublishedByAPIKeyID  string
}

type StoredWorkflowRevisionListOptions struct {
	Limit  int
	Offset int
}

const maxStoredWorkflowRevisionOffset = int(^uint32(0) >> 1)

func validStoredWorkflowRevisionListOptions(opts StoredWorkflowRevisionListOptions) bool {
	return opts.Limit > 0 && opts.Limit <= 100 && opts.Offset >= 0 && opts.Offset <= maxStoredWorkflowRevisionOffset
}

type StoredWorkflowMutation struct {
	TestSuite        json.RawMessage
	CheckReceipt     string
	CheckEvidence    *api.StoredWorkflowCheckEvidence
	Action           string
	ExpectedVersion  int64
	Draft            json.RawMessage
	Enabled          bool
	TakeOverManifest bool
	RestoreManifest  bool
	ActorAccountID   string
	ActorAPIKeyID    string
}

type StoredWorkflowStore interface {
	ListStoredWorkflows(context.Context, string) ([]StoredWorkflow, error)
	ListStoredWorkflowRevisions(context.Context, string, string, StoredWorkflowRevisionListOptions) ([]StoredWorkflowRevision, int, error)
	GetStoredWorkflowRevision(context.Context, string, string, int64) (StoredWorkflowRevision, error)
	MutateStoredWorkflow(context.Context, string, string, StoredWorkflowMutation) (StoredWorkflow, error)
	EffectiveWorkflowDefinitions(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

var (
	ErrStoredWorkflowVersionConflict   = errors.New("stored workflow: version changed; reload before editing")
	ErrStoredWorkflowOwnershipConflict = errors.New("stored workflow: explicit confirmation is required to change YAML ownership")
	ErrStoredWorkflowInvalid           = errors.New("stored workflow: invalid definition")
	ErrStoredWorkflowRevisionNotFound  = errors.New("stored workflow: revision not found")
)

type StoredWorkflowQuotaError struct {
	Plan            api.Plan
	Limit, Observed int
}

func (e *StoredWorkflowQuotaError) Error() string {
	return fmt.Sprintf("stored workflow: definition quota exceeded (%d/%d)", e.Observed, e.Limit)
}

func copyStoredWorkflow(a StoredWorkflow) StoredWorkflow {
	a.TestSuite, a.TestReport = cloneWorkflowJSON(a.TestSuite), cloneWorkflowJSON(a.TestReport)
	a.Draft = cloneWorkflowJSON(a.Draft)
	a.Published = cloneWorkflowJSON(a.Published)
	return a
}

func copyStoredWorkflowRevision(revision StoredWorkflowRevision) StoredWorkflowRevision {
	revision.Definition = cloneWorkflowJSON(revision.Definition)
	revision.CheckEvidence = cloneWorkflowJSON(revision.CheckEvidence)
	return revision
}

// Dashboard publication owns one name, including while paused. Unpublished
// drafts do not change deployed YAML intent. Both paths emit WorkflowSpec.
func mergeStoredWorkflowDefinitions(manifest json.RawMessage, records []StoredWorkflow) (json.RawMessage, error) {
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

func mutateStoredWorkflow(appID, name string, mutation StoredWorkflowMutation, previous *StoredWorkflow, manifest json.RawMessage, records []StoredWorkflow, plan api.Plan, hasDeployment bool) (StoredWorkflow, error) {
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\\x00") || len(name) > api.StoredWorkflowNameMaxBytes || mutation.ExpectedVersion < 0 {
		return StoredWorkflow{}, ErrStoredWorkflowInvalid
	}
	current := StoredWorkflow{AppID: appID, Name: name, Enabled: true}
	if previous != nil {
		current = copyStoredWorkflow(*previous)
	}
	if current.Version != mutation.ExpectedVersion {
		return StoredWorkflow{}, ErrStoredWorkflowVersionConflict
	}
	var deployed []api.WorkflowSpec
	if len(manifest) > 0 {
		if err := json.Unmarshal(manifest, &deployed); err != nil {
			return StoredWorkflow{}, err
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
		return StoredWorkflow{}, &StoredWorkflowQuotaError{plan, plan.WorkflowMaxPerApp(), len(names)}
	}
	switch mutation.Action {
	case "tests":
		if previous == nil {
			return StoredWorkflow{}, ErrNotFound
		}
		if err := validateStoredWorkflowTestSuite(mutation.TestSuite); err != nil {
			return StoredWorkflow{}, err
		}
		current.TestSuite = cloneWorkflowJSON(mutation.TestSuite)
		if len(current.TestSuite) == 0 {
			current.TestReport = nil
		}
	case "save":
		if len(mutation.Draft) == 0 || int64(len(mutation.Draft)) > api.StoredWorkflowDefinitionMaxBytes {
			return StoredWorkflow{}, ErrStoredWorkflowInvalid
		}
		var definition api.WorkflowSpec
		if err := json.Unmarshal(mutation.Draft, &definition); err != nil || definition.Name != name {
			return StoredWorkflow{}, ErrStoredWorkflowInvalid
		}
		if definition.Steps == nil {
			definition.Steps = []api.WorkflowStepSpec{}
		}
		current.Draft, _ = json.Marshal(definition)
	case "publish":
		if mutation.CheckEvidence != nil && mutation.CheckEvidence.ServerVerified && mutation.CheckReceipt == "" {
			return StoredWorkflow{}, ErrStoredWorkflowInvalid
		}
		if previous == nil {
			return StoredWorkflow{}, ErrNotFound
		}
		if !hasDeployment {
			return StoredWorkflow{}, ErrWorkflowEventTargetUnavailable
		}
		if len(names) > plan.WorkflowMaxPerApp() {
			return StoredWorkflow{}, &StoredWorkflowQuotaError{plan, plan.WorkflowMaxPerApp(), len(names)}
		}
		var definition api.WorkflowSpec
		if err := json.Unmarshal(current.Draft, &definition); err != nil {
			return StoredWorkflow{}, fmt.Errorf("%w: %w", ErrStoredWorkflowInvalid, err)
		}
		if err := mutation.CheckEvidence.Validate(definition, current.Version, time.Now()); err != nil {
			return StoredWorkflow{}, fmt.Errorf("%w: %w", ErrStoredWorkflowInvalid, err)
		}
		if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
			return StoredWorkflow{}, fmt.Errorf("%w: %w", ErrStoredWorkflowInvalid, err)
		}
		if current.PublishedVersion == 0 && fromManifest && !mutation.TakeOverManifest {
			return StoredWorkflow{}, ErrStoredWorkflowOwnershipConflict
		}
		if current.PublishedVersion == 0 {
			current.Enabled = definition.Trigger == nil || definition.Trigger.Enabled == nil || *definition.Trigger.Enabled
		}
		current.Published = cloneWorkflowJSON(current.Draft)
		current.PublishedVersion = current.Version + 1
	case "enable":
		if previous == nil || len(current.Published) == 0 {
			return StoredWorkflow{}, ErrNotFound
		}
		if mutation.Enabled {
			if !plan.WorkflowsAllowed() {
				return StoredWorkflow{}, api.ErrPlanWorkflowsNotAllowed(plan)
			}
			var definition api.WorkflowSpec
			if err := json.Unmarshal(current.Published, &definition); err != nil {
				return StoredWorkflow{}, ErrStoredWorkflowInvalid
			}
			if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
				return StoredWorkflow{}, fmt.Errorf("%w: %w", ErrStoredWorkflowInvalid, err)
			}
		}
		current.Enabled = mutation.Enabled
	case "delete":
		if previous == nil {
			return StoredWorkflow{}, ErrNotFound
		}
		if current.PublishedVersion > 0 && fromManifest && !mutation.RestoreManifest {
			return StoredWorkflow{}, ErrStoredWorkflowOwnershipConflict
		}
	default:
		return StoredWorkflow{}, ErrInvalidArgument
	}
	current.Version++
	current.UpdatedAt = time.Now().UTC()
	return current, nil
}

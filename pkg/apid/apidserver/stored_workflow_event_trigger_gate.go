package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// eventTriggerNeedsEvents reports whether a trigger can match an event that
// the events capability gates: any source outside the platform's gregale.*
// namespace. Wildcards sit only at a pattern's edges, so a pattern that
// starts with the literal "gregale." matches platform sources only.
func eventTriggerNeedsEvents(trigger *api.WorkflowTriggerSpec) bool {
	if trigger == nil || trigger.Type != "event" {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(trigger.Source), "gregale.")
}

// storedWorkflowEventsRefusal returns the problem that refuses a publish or
// enable of the stored workflow name in app, or nil when it may proceed.
func (s *server) storedWorkflowEventsRefusal(ctx context.Context, store state.StoredWorkflowStore, account state.Account, app state.App, name string, mutation state.StoredWorkflowMutation) *api.Problem {
	turnsOn := mutation.Action == "publish" || mutation.Action == "enable" && mutation.Enabled
	if s.capGate == nil || !turnsOn {
		return nil
	}
	if s.capGate.Enabled("events", appFlagSubject(account, app)) {
		return nil
	}
	records, err := store.ListStoredWorkflows(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("failed to read stored workflow")
	}
	for _, record := range records {
		if record.Name != name {
			continue
		}
		raw := record.Draft
		if mutation.Action == "enable" {
			raw = record.Published
		}
		def, ok := decodeStoredWorkflowSpec(raw)
		if !ok || !eventTriggerNeedsEvents(def.Trigger) {
			return nil
		}
		return api.NewProblem(http.StatusConflict, api.CodeStoredWorkflowEventsUnavailable, "Custom events are not enabled",
			"This stored workflow starts on events from source \""+def.Trigger.Source+"\", and custom events are not enabled for this account, so it would never start. "+
				"Use a gregale.* platform event source, a schedule, a manual start or an inbound webhook binding instead.").
			WithDocs("https://gregale.dev/docs/stored-workflows#start-on-an-event")
	}
	return nil
}

// decodeStoredWorkflowSpec decodes a saved definition; ok is false when it
// is empty or unreadable, which MutateStoredWorkflow itself refuses.
func decodeStoredWorkflowSpec(raw []byte) (api.WorkflowSpec, bool) {
	var def api.WorkflowSpec
	if len(raw) == 0 {
		return def, false
	}
	return def, json.Unmarshal(raw, &def) == nil
}

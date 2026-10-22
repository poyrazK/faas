package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplicationStandardRuntimeAdmissionEnvelope(t *testing.T) {
	app := state.App{ID: uuid.NewString(), OrgID: uuid.NewString(), ProjectID: uuid.NewString()}
	ready := state.ApplicationStandardEnrollment{AppID: app.ID, OrgID: app.OrgID, ProjectID: app.ProjectID, State: "persisted", DesiredRevision: 3, PersistedRevision: 3, EffectiveHash: strings.Repeat("a", 64)}
	for _, tc := range []struct {
		name   string
		mutate func(*state.ApplicationStandardEnrollment)
		want   bool
	}{
		{"persisted without runtime observation", func(e *state.ApplicationStandardEnrollment) { e.ObservedRevision = 0 }, true},
		{"observed", func(e *state.ApplicationStandardEnrollment) { e.State = "observed"; e.ObservedRevision = 3 }, true},
		{"pending", func(e *state.ApplicationStandardEnrollment) { e.State = "pending" }, false},
		{"applying", func(e *state.ApplicationStandardEnrollment) { e.State = "applying" }, false},
		{"blocked", func(e *state.ApplicationStandardEnrollment) { e.State = "blocked" }, false},
		{"missing", func(e *state.ApplicationStandardEnrollment) { *e = state.ApplicationStandardEnrollment{} }, false},
		{"wrong app", func(e *state.ApplicationStandardEnrollment) { e.AppID = uuid.NewString() }, false},
		{"wrong organization", func(e *state.ApplicationStandardEnrollment) { e.OrgID = uuid.NewString() }, false},
		{"old project", func(e *state.ApplicationStandardEnrollment) { e.ProjectID = uuid.NewString() }, false},
		{"project removal not repaired", func(e *state.ApplicationStandardEnrollment) { e.ProjectID = "" }, false},
		{"old installation", func(e *state.ApplicationStandardEnrollment) { e.PersistedRevision = 2 }, false},
		{"future installation", func(e *state.ApplicationStandardEnrollment) { e.PersistedRevision = 4 }, false},
		{"missing projection hash", func(e *state.ApplicationStandardEnrollment) { e.EffectiveHash = "" }, false},
		{"missing desired revision", func(e *state.ApplicationStandardEnrollment) { e.DesiredRevision = 0; e.PersistedRevision = 0 }, false},
		{"UUID aliases", func(e *state.ApplicationStandardEnrollment) {
			e.AppID = strings.ToUpper(e.AppID)
			e.OrgID = strings.ReplaceAll(e.OrgID, "-", "")
		}, true},
		{"unmanaged baseline", func(e *state.ApplicationStandardEnrollment) {
			e.State = "unmanaged"
			e.PersistedRevision = 0
			e.EffectiveHash = ""
		}, true},
		{"unmanaged with admission pin", func(e *state.ApplicationStandardEnrollment) {
			e.State = "unmanaged"
			e.Adoptions = []appstandards.Adoption{{AssignmentID: uuid.NewString(), Version: 1}}
		}, false},
		{"unmanaged with controls to restore", func(e *state.ApplicationStandardEnrollment) {
			e.State = "unmanaged"
			e.MaterializedFields = []appstandards.Field{appstandards.EgressCIDRs}
		}, false},
		{"unmanaged with retained installation", func(e *state.ApplicationStandardEnrollment) {
			e.State = "unmanaged"
		}, false},
		{"unmanaged with invalid negative installation", func(e *state.ApplicationStandardEnrollment) {
			e.State = "unmanaged"
			e.PersistedRevision = -1
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := state.CloneApplicationStandardEnrollment(ready)
			tc.mutate(&e)
			if got := state.ApplicationStandardEnrollmentPermitsRuntime(app, e); got != tc.want {
				t.Fatalf("permitted=%v want=%v: %+v", got, tc.want, e)
			}
		})
	}
}

package state

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

func TestApplicationStandardRuntimeAdmissionEnvelope(t *testing.T) {
	app := App{ID: uuid.NewString(), OrgID: uuid.NewString(), ProjectID: uuid.NewString()}
	ready := ApplicationStandardEnrollment{AppID: app.ID, OrgID: app.OrgID, ProjectID: app.ProjectID, State: "persisted", DesiredRevision: 3, PersistedRevision: 3, EffectiveHash: strings.Repeat("a", 64)}
	for _, tc := range []struct {
		name   string
		mutate func(*ApplicationStandardEnrollment)
		want   bool
	}{
		{"persisted without runtime observation", func(e *ApplicationStandardEnrollment) { e.ObservedRevision = 0 }, true},
		{"observed", func(e *ApplicationStandardEnrollment) { e.State = "observed"; e.ObservedRevision = 3 }, true},
		{"pending", func(e *ApplicationStandardEnrollment) { e.State = "pending" }, false},
		{"applying", func(e *ApplicationStandardEnrollment) { e.State = "applying" }, false},
		{"blocked", func(e *ApplicationStandardEnrollment) { e.State = "blocked" }, false},
		{"missing", func(e *ApplicationStandardEnrollment) { *e = ApplicationStandardEnrollment{} }, false},
		{"wrong app", func(e *ApplicationStandardEnrollment) { e.AppID = uuid.NewString() }, false},
		{"wrong organization", func(e *ApplicationStandardEnrollment) { e.OrgID = uuid.NewString() }, false},
		{"old project", func(e *ApplicationStandardEnrollment) { e.ProjectID = uuid.NewString() }, false},
		{"project removal not repaired", func(e *ApplicationStandardEnrollment) { e.ProjectID = "" }, false},
		{"old installation", func(e *ApplicationStandardEnrollment) { e.PersistedRevision = 2 }, false},
		{"future installation", func(e *ApplicationStandardEnrollment) { e.PersistedRevision = 4 }, false},
		{"missing projection hash", func(e *ApplicationStandardEnrollment) { e.EffectiveHash = "" }, false},
		{"missing desired revision", func(e *ApplicationStandardEnrollment) { e.DesiredRevision = 0; e.PersistedRevision = 0 }, false},
		{"UUID aliases", func(e *ApplicationStandardEnrollment) {
			e.AppID = strings.ToUpper(e.AppID)
			e.OrgID = strings.ReplaceAll(e.OrgID, "-", "")
		}, true},
		{"unmanaged baseline", func(e *ApplicationStandardEnrollment) {
			e.State = "unmanaged"
			e.PersistedRevision = 0
			e.EffectiveHash = ""
		}, true},
		{"unmanaged with admission pin", func(e *ApplicationStandardEnrollment) {
			e.State = "unmanaged"
			e.Adoptions = []appstandards.Adoption{{AssignmentID: uuid.NewString(), Version: 1}}
		}, false},
		{"unmanaged with controls to restore", func(e *ApplicationStandardEnrollment) {
			e.State = "unmanaged"
			e.MaterializedFields = []appstandards.Field{appstandards.EgressCIDRs}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := cloneApplicationStandardEnrollment(ready)
			tc.mutate(&e)
			if got := ApplicationStandardEnrollmentPermitsRuntime(app, e); got != tc.want {
				t.Fatalf("permitted=%v want=%v: %+v", got, tc.want, e)
			}
		})
	}
}

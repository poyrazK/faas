package state

import (
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	objsql "github.com/onebox-faas/faas/pkg/state/scenarioobj/sqlc"
)

func scenarioObjectCaller(v objsql.GetForAppRow, err error) (rehearsalobj.Resource, error) {
	r, e := scenarioObjectDocument(v.Document, err)
	if e != nil {
		return r, e
	}
	r.CallerPermission = v.CallerPermission
	r.CallerWorkload = v.CallerWorkload
	r.CallerDeploymentID = v.CallerDeploymentID
	return r, nil
}
func (m *MemStore) scenarioObjectPermissionLocked(r rehearsalobj.Resource, app string) string {
	p, ok := m.scenarioActionPolicies[app]
	if !ok || p.Member.AccountID != r.AccountID || p.Member.RunID != r.RunID {
		return ""
	}
	if p.Member.ManagedWorkJSON == "" {
		if app == r.AppID {
			return "read_write"
		}
		return ""
	}
	part, _ := rehearsalwork.Decode(p.Member.ManagedWorkJSON).Participant(p.Member.Workload)
	_, count := m.managedDeploymentLocked(app)
	if count != 1 {
		return ""
	}
	return part.Objects
}

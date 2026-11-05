package state

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/eventcontract"
)

// recipientsInProducerReachLocked mirrors event_recipients_in_producer_reach
// (ADR-20261025211616). A company app's rows stay under its creator's account,
// so an account's fanout spans its personal apps and the companies it created
// apps for. An event reaches recipients in its producer's company (or the
// personal domain) always, and crosses into another domain only while the
// account's owner is an active owner, admin or developer of every company on
// both sides. An event without a producer routes as before.
func (m *MemStore) recipientsInProducerReachLocked(account, producer string, recipients []PublishedEventRecipient) ([]PublishedEventRecipient, error) {
	if producer == "" || len(recipients) == 0 {
		return recipients, nil
	}
	var producerCompany string
	if producer != eventcontract.ProducerPersonal {
		kind, id, _ := strings.Cut(producer, ":")
		switch {
		case parseSubjectID(id) == nil:
			return nil, fmt.Errorf("event producer %q is not recognized", producer)
		case kind == "org":
			if org, ok := m.eventOrgLocked(id); ok && !org.Personal {
				producerCompany = org.ID
			}
		case kind == "app":
			producerCompany = m.eventAppCompanyLocked(id)
		default:
			return nil, fmt.Errorf("event producer %q is not recognized", producer)
		}
	}
	producerReachable := producerCompany == "" || m.eventCompanyReachableLocked(account, producerCompany)
	kept := make([]PublishedEventRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		company := m.eventAppCompanyLocked(recipient.AppID)
		if company == producerCompany || producerReachable && (company == "" || m.eventCompanyReachableLocked(account, company)) {
			kept = append(kept, recipient)
		}
	}
	return kept, nil
}

// eventAppCompanyLocked is the company an app belongs to, or "" for a
// personal app.
func (m *MemStore) eventAppCompanyLocked(appID string) string {
	app, ok := m.apps[appID]
	if !ok {
		for id, candidate := range m.apps {
			if sameMemUUID(id, appID) {
				app, ok = candidate, true
				break
			}
		}
	}
	if !ok || app.OrgID == "" {
		return ""
	}
	if org, ok := m.eventOrgLocked(app.OrgID); ok && !org.Personal {
		return org.ID
	}
	return ""
}

func (m *MemStore) eventOrgLocked(orgID string) (Org, bool) {
	if org, ok := m.orgs[orgID]; ok {
		return org, true
	}
	for id, org := range m.orgs {
		if sameMemUUID(id, orgID) {
			return org, true
		}
	}
	return Org{}, false
}

func (m *MemStore) eventCompanyReachableLocked(account, company string) bool {
	org, ok := m.eventOrgLocked(company)
	if !ok || org.DeletedPending || org.Status == OrgStatusDeletedPending {
		return false
	}
	for key, member := range m.memberships {
		if key.OrgID != org.ID || !sameMemUUID(key.AccountID, account) || member.RemovedAt != nil {
			continue
		}
		return member.Role == OrgRoleOwner || member.Role == OrgRoleAdmin || member.Role == OrgRoleDeveloper
	}
	return false
}

// eventAppInProducerReachLocked mirrors event_app_in_producer_reach: replays
// and their previews offer an app only the retained events fanout would route
// to it now.
func (m *MemStore) eventAppInProducerReachLocked(account string, payload []byte, appID string) bool {
	var event publishedEventIdentity
	if json.Unmarshal(payload, &event) != nil {
		return false
	}
	kept, err := m.recipientsInProducerReachLocked(account, event.Producer, []PublishedEventRecipient{{AppID: appID}})
	return err == nil && len(kept) == 1
}

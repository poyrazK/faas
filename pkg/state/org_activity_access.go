package state

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func bindOrgActivityToInvitation(entry OrgActivity, invitation OrgInvitation) (OrgActivity, error) {
	orgID, err := uuid.Parse(invitation.OrgID)
	if err != nil {
		return OrgActivity{}, errors.New("state: invitation activity requires an organization id")
	}
	entry.OrgID = orgID
	entry.ResourceType = "invitation"
	entry.ResourceID = invitation.ID
	if label := strings.TrimSpace(invitation.Email); label != "" {
		entry.ResourceLabel = label
	} else if strings.TrimSpace(entry.ResourceLabel) == "" {
		entry.ResourceLabel = "invitation"
	}
	if entry.SourceID == "" {
		entry.SourceID = entry.Kind + ":" + invitation.ID
	}
	return normalizeOrgActivity(entry, time.Now())
}

func bindOrgActivityToMembership(entry OrgActivity, membership OrgMembership, label string) (OrgActivity, error) {
	orgID, err := uuid.Parse(membership.OrgID)
	if err != nil {
		return OrgActivity{}, errors.New("state: member activity requires an organization id")
	}
	entry.OrgID = orgID
	entry.ResourceType = "member"
	entry.ResourceID = membership.AccountID
	if strings.TrimSpace(label) != "" {
		entry.ResourceLabel = strings.TrimSpace(label)
	} else if strings.TrimSpace(entry.ResourceLabel) == "" {
		entry.ResourceLabel = "member"
	}
	if entry.SourceID == "" {
		entry.SourceID = entry.Kind + ":" + uuid.NewString()
	}
	return normalizeOrgActivity(entry, time.Now())
}

func bindOrgActivityToOwnershipTransfer(entry OrgActivity, from, to OrgMembership, label string) (OrgActivity, error) {
	entry, err := withOrgActivityData(entry, map[string]any{
		"previous_owner_account_id": from.AccountID,
		"new_owner_account_id":      to.AccountID,
	})
	if err != nil {
		return OrgActivity{}, err
	}
	return bindOrgActivityToMembership(entry, to, label)
}

func withOrgActivityData(entry OrgActivity, values map[string]any) (OrgActivity, error) {
	data := make(map[string]json.RawMessage)
	if len(entry.Data) != 0 {
		if err := json.Unmarshal(entry.Data, &data); err != nil || data == nil {
			return OrgActivity{}, errors.New("state: member activity data must be a JSON object")
		}
	}
	for key, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return OrgActivity{}, err
		}
		data[key] = encoded
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return OrgActivity{}, err
	}
	entry.Data = encoded
	return entry, nil
}

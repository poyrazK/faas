package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Issue teams are app-scoped routing targets. Membership never grants app access.
type IssueTeam struct {
	ID                 string    `json:"id"`
	Key                string    `json:"key"`
	Name               string    `json:"name"`
	MemberAccountIDs   []string  `json:"member_account_ids"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	UpdatedByAccountID string    `json:"updated_by_account_id,omitempty"`
}
type PutIssueTeamRequest struct {
	Name             string   `json:"name"`
	MemberAccountIDs []string `json:"member_account_ids"`
}
type ListIssueTeamsResponse struct {
	Items []IssueTeam `json:"items"`
}

func NormalizeIssueTeam(in PutIssueTeamRequest) (PutIssueTeamRequest, error) {
	view, err := NormalizeIssueSavedView(PutIssueSavedViewRequest{Name: in.Name})
	if err != nil {
		return in, err
	}
	in.Name = view.Name
	if len(in.MemberAccountIDs) == 0 || len(in.MemberAccountIDs) > IssueTeamMembersMax {
		return in, fmt.Errorf("teams require 1–%d distinct members", IssueTeamMembersMax)
	}
	seen := map[string]bool{}
	in.MemberAccountIDs = append([]string(nil), in.MemberAccountIDs...)
	for i, raw := range in.MemberAccountIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || id == uuid.Nil || seen[id.String()] {
			return in, fmt.Errorf("members must be distinct nonzero account UUIDs")
		}
		in.MemberAccountIDs[i] = id.String()
		seen[id.String()] = true
	}
	return in, nil
}

// NormalizeIssueAssignment permits one account, one team, or neither (unassign).
func NormalizeIssueAssignment(account, team string) (string, string, error) {
	account, team = strings.TrimSpace(account), strings.TrimSpace(team)
	if account != "" && team != "" {
		return "", "", fmt.Errorf("choose one account or one team")
	}
	for _, p := range []*string{&account, &team} {
		if *p != "" {
			id, err := uuid.Parse(*p)
			if err != nil || id == uuid.Nil {
				return "", "", fmt.Errorf("assignment requires a nonzero account or team UUID")
			}
			*p = id.String()
		}
	}
	return account, team, nil
}

package api

import (
	"net/url"
	"strconv"
	"time"
)

type IssueSavedViewFilters struct {
	Team         string `json:"team,omitempty"`
	State        string `json:"state,omitempty"`
	Environment  string `json:"environment,omitempty"`
	Assignee     string `json:"assignee,omitempty"`
	Sort         string `json:"sort,omitempty"`
	MinCustomers int64  `json:"min_customers,omitempty"`
	Tag          string `json:"tag,omitempty"`
}

type PutIssueSavedViewRequest struct {
	Name    string                `json:"name"`
	Filters IssueSavedViewFilters `json:"filters"`
}

type IssueSavedView struct {
	Key                string                `json:"key"`
	Name               string                `json:"name"`
	Filters            IssueSavedViewFilters `json:"filters"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
	UpdatedByAccountID string                `json:"updated_by_account_id,omitempty"`
}

type ListIssueSavedViewsResponse struct {
	Items []IssueSavedView `json:"items"`
}

func (f IssueSavedViewFilters) Query() url.Values {
	return url.Values{"team": {f.Team}, "state": {f.State}, "environment": {f.Environment}, "assignee": {f.Assignee}, "sort": {f.Sort}, "min_customers": {strconv.FormatInt(f.MinCustomers, 10)}, "tag": {f.Tag}}
}

package api

import (
	"time"

	"github.com/onebox-faas/faas/pkg/appstandards"
)

// Destinations are immutable. Changing an endpoint or credential creates a
// new reference which must be adopted through a reviewed standard version.
type CreateApplicationStandardLogDestinationRequest struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	TargetURL  string `json:"target_url"`
	AuthHeader string `json:"auth_header,omitempty"`
}

func (r *CreateApplicationStandardLogDestinationRequest) UnmarshalJSON(raw []byte) error {
	type wire CreateApplicationStandardLogDestinationRequest
	var value wire
	if err := appstandards.DecodeStrict(raw, &value); err != nil {
		return err
	}
	*r = CreateApplicationStandardLogDestinationRequest(value)
	return nil
}

type ApplicationStandardLogDestination struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	TargetURL     string    `json:"target_url"`
	HasAuthHeader bool      `json:"has_auth_header"`
	ConfigHash    string    `json:"config_hash"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

type ApplicationStandardLogDestinationList struct {
	Destinations  []ApplicationStandardLogDestination `json:"destinations"`
	NextPageAfter string                              `json:"next_page_after,omitempty"`
}

type CreateApplicationStandardPublisherRequest struct {
	Name         string `json:"name"`
	PublicKeyDER string `json:"public_key_der"` // base64 ECDSA P-256 SubjectPublicKeyInfo
}

func (r *CreateApplicationStandardPublisherRequest) UnmarshalJSON(raw []byte) error {
	type wire CreateApplicationStandardPublisherRequest
	var value wire
	if err := appstandards.DecodeStrict(raw, &value); err != nil {
		return err
	}
	*r = CreateApplicationStandardPublisherRequest(value)
	return nil
}

type ApplicationStandardPublisher struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Name         string    `json:"name"`
	PublicKeyDER string    `json:"public_key_der"`
	Fingerprint  string    `json:"fingerprint"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

type ApplicationStandardPublisherList struct {
	Publishers    []ApplicationStandardPublisher `json:"publishers"`
	NextPageAfter string                         `json:"next_page_after,omitempty"`
}

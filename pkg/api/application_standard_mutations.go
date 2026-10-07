package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

// Settings and additional destinations are complete replacement intent.
// Explicit empty values clear local choices and inherit the current standard.
type SetApplicationStandardLocalIntentRequest struct {
	ExpectedRevision          int64           `json:"expected_revision"`
	Settings                  json.RawMessage `json:"settings"`
	AdditionalLogDestinations []string        `json:"additional_log_destinations"`
}

func (r *SetApplicationStandardLocalIntentRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		ExpectedRevision          *int64          `json:"expected_revision"`
		Settings                  json.RawMessage `json:"settings"`
		AdditionalLogDestinations *[]string       `json:"additional_log_destinations"`
	}
	if err := appstandards.DecodeStrict(raw, &wire); err != nil {
		return err
	}
	if wire.ExpectedRevision == nil || len(wire.Settings) == 0 || wire.AdditionalLogDestinations == nil {
		return fmt.Errorf("expected_revision, settings and additional_log_destinations are required")
	}
	*r = SetApplicationStandardLocalIntentRequest{*wire.ExpectedRevision, wire.Settings, *wire.AdditionalLogDestinations}
	return r.Validate()
}

func (r SetApplicationStandardLocalIntentRequest) Validate() error {
	if err := validateStandardExpectedRevision(r.ExpectedRevision); err != nil {
		return err
	}
	if _, err := appstandards.ParseSettings(r.Settings, ApplicationStandardResolverLimits()); err != nil {
		return err
	}
	if r.AdditionalLogDestinations == nil {
		return fmt.Errorf("additional_log_destinations must be an explicit array")
	}
	ids, _ := json.Marshal(r.AdditionalLogDestinations)
	raw, _ := json.Marshal(appstandards.Settings{appstandards.LogDestinations: ids})
	_, err := appstandards.ParseSettings(raw, ApplicationStandardResolverLimits())
	return err
}

type ApproveApplicationStandardExceptionRequest struct {
	ExpectedRevision int64              `json:"expected_revision"`
	StandardID       string             `json:"standard_id"`
	Version          int64              `json:"version"`
	Field            appstandards.Field `json:"field"`
	Value            json.RawMessage    `json:"value"`
	Reason           string             `json:"reason"`
	ExpiresAt        time.Time          `json:"expires_at"`
}

func (r *ApproveApplicationStandardExceptionRequest) UnmarshalJSON(raw []byte) error {
	type request ApproveApplicationStandardExceptionRequest
	var wire request
	if err := appstandards.DecodeStrict(raw, &wire); err != nil {
		return err
	}
	*r = ApproveApplicationStandardExceptionRequest(wire)
	return r.Validate()
}

func (r ApproveApplicationStandardExceptionRequest) Validate() error {
	if err := validateStandardExpectedRevision(r.ExpectedRevision); err != nil {
		return err
	}
	id, err := uuid.Parse(r.StandardID)
	if err != nil || id == uuid.Nil || r.Version < 1 || r.Version > ApplicationStandardMaxVersion || r.ExpiresAt.IsZero() || strings.TrimSpace(r.Reason) == "" || !utf8.ValidString(r.Reason) || len(strings.TrimSpace(r.Reason)) > ApplicationStandardMaxDescriptionBytes {
		return fmt.Errorf("invalid exception identity, version, reason or expiry")
	}
	raw, err := json.Marshal(appstandards.Settings{r.Field: r.Value})
	if err != nil {
		return err
	}
	_, err = appstandards.ParseSettings(raw, ApplicationStandardResolverLimits())
	return err
}

type RevokeApplicationStandardExceptionRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

func (r *RevokeApplicationStandardExceptionRequest) UnmarshalJSON(raw []byte) error {
	type request RevokeApplicationStandardExceptionRequest
	var wire request
	if err := appstandards.DecodeStrict(raw, &wire); err != nil {
		return err
	}
	*r = RevokeApplicationStandardExceptionRequest(wire)
	return validateStandardExpectedRevision(r.ExpectedRevision)
}

func validateStandardExpectedRevision(revision int64) error {
	if revision < 1 || revision >= ApplicationStandardMaxVersion {
		return fmt.Errorf("expected_revision is outside the application standard revision bounds")
	}
	return nil
}

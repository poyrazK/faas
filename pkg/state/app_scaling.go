// adr: 20261104104521
package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrScalingRevisionConflict = errors.New("scaling revision changed")
var ErrScalingRequestConflict = errors.New("scaling request ID reused")

// AppScalingStore atomically guards the reviewed settings and records the result.
// Validate runs against the locked app and must not perform I/O.
type AppScalingStore interface {
	AppScaling(context.Context, string, string, string) (api.AppScalingSnapshot, error)
	ApplyAppScaling(context.Context, AppScalingChange) (api.AppScalingReceipt, error)
	AppScalingReceipt(context.Context, string, string, string, string) (api.AppScalingReceipt, error)
}

type AppScalingChange struct {
	AccountID, AppID, Scope string
	Request                 api.ApplyAppScalingRequest
	Validate                func(App) error
}

type scalingRecord struct {
	Hash    [32]byte
	Receipt api.AppScalingReceipt
}

func scalingSettings(app App) api.AppScalingSettings {
	return api.AppScalingSettings{RAMMB: app.RAMMB, CPUMillicores: app.CPUMillicores, MaxConcurrency: app.MaxConcurrency, MinInstances: app.MinInstances}
}

func scalingParams(req api.ApplyAppScalingRequest) UpdateAppParams {
	return UpdateAppParams{RAMMB: req.RAMMB, CPUMillicores: req.CPUMillicores, MaxConcurrency: req.MaxConcurrency, MinInstances: req.MinInstances, SetMinInstances: req.MinInstances != nil}
}

// Pin all four reviewed values when synchronizing production's desired spec.
// A legacy floor writer may have advanced the app row without updating that spec;
// merging an omitted value from the older spec must not change the receipt.
func reviewedScalingParams(app App, req api.ApplyAppScalingRequest) UpdateAppParams {
	p := scalingParams(req)
	if p.RAMMB == nil {
		p.RAMMB = &app.RAMMB
	}
	if p.CPUMillicores == nil {
		p.CPUMillicores = &app.CPUMillicores
	}
	if p.MaxConcurrency == nil {
		p.MaxConcurrency = &app.MaxConcurrency
	}
	if p.MinInstances == nil {
		p.MinInstances = &app.MinInstances
	}
	p.SetMinInstances = true
	return p
}

func validateScalingChange(in *AppScalingChange) ([32]byte, error) {
	if in.Scope == "" {
		in.Scope = "default"
	}
	if in.AccountID == "" || in.AppID == "" || (in.Scope != "default" && (!api.ValidProjectEnvironmentSlug(in.Scope) || IsProductionScope(in.Scope))) {
		return [32]byte{}, ErrInvalidArgument
	}
	if problem := in.Request.Validate(); problem != nil {
		return [32]byte{}, ErrInvalidArgument
	}
	if !scalingIdentityMatches(in.AppID, in.Request.ExpectedAppID) {
		return [32]byte{}, ErrConflict
	}
	if (in.Scope == "default") != (in.Request.ExpectedEnvironmentID == "") {
		return [32]byte{}, ErrInvalidArgument
	}
	raw, err := json.Marshal(in.Request)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

// MemStore's historical IDs omit UUID separators; compare UUID identity rather
// than its wire spelling, as public SDKs canonicalize UUIDs.
func scalingIdentityMatches(actual, expected string) bool {
	a, err := uuid.Parse(actual)
	if err != nil {
		return false
	}
	b, err := uuid.Parse(expected)
	return err == nil && a == b
}

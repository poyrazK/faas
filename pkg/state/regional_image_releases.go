package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrRegionalImageReleaseConflict = errors.New("regional image release already identifies another image")
	ErrRegionalImageReleaseExpired  = errors.New("regional image release deployment was collected")
	ErrRegionalImageReleaseOrder    = errors.New("regional image release generation is stale or stream identity conflicts")
)

// RegionalImageReleaseIntent pins the owner observed by apid as well as the
// immutable app and release identities. Ownership is checked again under lock.
type RegionalImageReleaseIntent struct {
	CellID, AppID, ReleaseID, AccountID, OrgID, ImageRef string
	GlobalAppID                                          string
	GrantID                                              string
	Generation                                           int64
}

// RegionalImageReleaseReceipt survives deployment collection. An empty
// DeploymentID is a tombstone, never permission to execute the release again.
type RegionalImageReleaseReceipt struct {
	CellID, AppID, ReleaseID, AccountID, ImageRef string
	DeploymentID, DeploymentStatus                string
	CreatedAt                                     time.Time
	GlobalAppID                                   string
	Generation                                    int64
}

type RegionalImageReleaseResult struct {
	Receipt          RegionalImageReleaseReceipt
	Deployment       Deployment
	ActivityOutboxID int64
	Replayed         bool
}

type RegionalImageReleaseStore interface {
	RegionalImageReleaseReceipt(context.Context, string, string, string) (RegionalImageReleaseReceipt, error)
	CreateRegionalImageRelease(context.Context, RegionalImageReleaseIntent, Deployment, *OrgActivity) (RegionalImageReleaseResult, error)
}

type regionalImageReleaseMutation struct {
	intent   RegionalImageReleaseIntent
	receipt  RegionalImageReleaseReceipt
	replayed bool
}

func validRegionalReleaseIDs(cellID, appID, releaseID string) bool {
	for _, id := range []string{cellID, appID, releaseID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return false
		}
	}
	return true
}

func validateRegionalImageRelease(intent RegionalImageReleaseIntent, d Deployment) error {
	if !validRegionalImageReleaseOrdering(intent.GlobalAppID, intent.Generation) {
		return ErrInvalidArgument
	}
	account, err := uuid.Parse(intent.AccountID)
	if err != nil || account == uuid.Nil || !validRegionalReleaseIDs(intent.CellID, intent.AppID, intent.ReleaseID) {
		return ErrInvalidArgument
	}
	if intent.OrgID != "" {
		org, err := uuid.Parse(intent.OrgID)
		if err != nil || org == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	if len(intent.ImageRef) > api.CellImageReleaseImageMaxBytes || !api.ValidDeploymentImage(intent.ImageRef) ||
		d.AppID != intent.AppID || d.ImageDigest != intent.ImageRef || d.Kind != DeploymentKindImage || normalizedDeploymentScope(d.Scope) != "default" {
		return ErrInvalidArgument
	}
	return nil
}

func (m *regionalImageReleaseMutation) replay(receipt RegionalImageReleaseReceipt) error {
	if receipt.AccountID != m.intent.AccountID {
		return ErrNotFound
	}
	if receipt.ImageRef != m.intent.ImageRef || receipt.GlobalAppID != m.intent.GlobalAppID || receipt.Generation != m.intent.Generation {
		return ErrRegionalImageReleaseConflict
	}
	if receipt.DeploymentID == "" {
		return ErrRegionalImageReleaseExpired
	}
	m.receipt, m.replayed = receipt, true
	return nil
}

func (m *regionalImageReleaseMutation) accepted(d Deployment) {
	m.receipt = RegionalImageReleaseReceipt{
		CellID: m.intent.CellID, AppID: m.intent.AppID, ReleaseID: m.intent.ReleaseID,
		AccountID: m.intent.AccountID, ImageRef: m.intent.ImageRef,
		DeploymentID: d.ID, DeploymentStatus: string(d.Status), CreatedAt: d.CreatedAt,
		GlobalAppID: m.intent.GlobalAppID, Generation: m.intent.Generation,
	}
}

func validRegionalImageReleaseOrdering(globalAppID string, generation int64) bool {
	if globalAppID == "" {
		return generation == 0
	}
	id, err := uuid.Parse(globalAppID)
	return err == nil && id != uuid.Nil && id.String() == globalAppID && generation > 0 && generation <= api.GlobalImageReleaseGenerationMax
}

func (m *regionalImageReleaseMutation) checkHead(head RegionalImageReleaseReceipt) error {
	if head.CellID != m.intent.CellID || head.AccountID != m.intent.AccountID || head.GlobalAppID != m.intent.GlobalAppID || head.Generation >= m.intent.Generation {
		return ErrRegionalImageReleaseOrder
	}
	return nil
}

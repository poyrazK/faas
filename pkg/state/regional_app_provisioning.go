package state

// adr: 797
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRegionalProvisionExpired = errors.New("regional provisioned app was removed")

type RegionalAppProvision struct {
	ProvisionID string
	GlobalAppID string
	CellID      string
	Region      string
	AccountID   string
	AppID       string
	AppSlug     string
	Request     json.RawMessage
	CreatedAt   time.Time
}

type RegionalAppProvisionStore interface {
	RegionalAppProvisionReceipt(context.Context, string, string, string) (RegionalAppProvision, error)
	CreateRegionalAppProvision(context.Context, RegionalAppProvision, App, api.Limits, OrgActivity) (RegionalAppProvision, bool, int64, error)
}

// An absent quota head is independent only when provisioning identity is also absent.
type RegionalAppProvisionAdmissionReader interface {
	RegionalAppProvisioned(context.Context, string) (bool, error)
}

func (p RegionalAppProvision) valid() bool {
	for _, id := range []string{p.ProvisionID, p.GlobalAppID, p.CellID, p.AccountID} {
		u, err := uuid.Parse(id)
		if err != nil || u == uuid.Nil {
			return false
		}
	}
	return api.ValidComputeRegion(p.Region) && p.Region != "" && api.ValidGlobalAppSlug(p.AppSlug) && json.Valid(p.Request) && len(p.Request) <= api.GlobalDirectoryRequestMaxBytes
}

func (p RegionalAppProvision) matches(q RegionalAppProvision) bool {
	return p.ProvisionID == q.ProvisionID && p.GlobalAppID == q.GlobalAppID && p.CellID == q.CellID && p.Region == q.Region && p.AccountID == q.AccountID && p.AppSlug == q.AppSlug && bytes.Equal(p.Request, q.Request)
}

func canonicalProvisionRequest(raw []byte) (json.RawMessage, error) {
	var req api.CreateAppRequest
	if json.Unmarshal(raw, &req) != nil {
		return nil, ErrInvalidArgument
	}
	return json.Marshal(req)
}

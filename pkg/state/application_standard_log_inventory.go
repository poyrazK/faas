package state

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrApplicationStandardLogConsumerFenced = errors.New("state: application standard logging consumer session changed")

// Private node facts prove loaded configuration, separately from provider delivery.
// They never advance application observation or establish fleet membership.
type ApplicationStandardLogInventoryDrain struct {
	DrainID    string `json:"drain_id"`
	ConfigHash string `json:"config_hash"`
}

type ApplicationStandardLogInventory struct {
	OrgID           string                                 `json:"org_id"`
	AppID           string                                 `json:"app_id"`
	AccountID       string                                 `json:"account_id"`
	DesiredRevision int64                                  `json:"desired_revision"`
	EffectiveHash   string                                 `json:"effective_hash"`
	Drains          []ApplicationStandardLogInventoryDrain `json:"drains"`
}

type ApplicationStandardLogConsumerSession struct {
	NodeID     string `json:"node_id"`
	SessionID  string `json:"session_id"`
	Generation int64  `json:"generation"`
}

type ApplicationStandardLogInventoryObservation struct {
	ApplicationStandardLogInventory
	ApplicationStandardLogConsumerSession
	ObservedAt time.Time `json:"observed_at"`
}

type ApplicationStandardLogConsumerSnapshot struct {
	Drains      []AppLogDrain
	Inventories []ApplicationStandardLogInventory
}

type ApplicationStandardLogInventoryStore interface {
	LoadApplicationStandardLogConsumerSnapshot(context.Context) (ApplicationStandardLogConsumerSnapshot, error)
	RegisterApplicationStandardLogConsumer(context.Context, string, string) (ApplicationStandardLogConsumerSession, error)
	CheckApplicationStandardLogConsumer(context.Context, ApplicationStandardLogConsumerSession) error
	RecordApplicationStandardLogInventory(context.Context, ApplicationStandardLogConsumerSession, ApplicationStandardLogInventory) (ApplicationStandardLogInventoryObservation, error)
	ListApplicationStandardLogInventories(context.Context, string, string) ([]ApplicationStandardLogInventoryObservation, error)
}

func validStandardLogSession(s ApplicationStandardLogConsumerSession) bool {
	return validStandardResourceRead(s.NodeID, s.SessionID) && s.Generation > 0
}

func validStandardLogInventory(i ApplicationStandardLogInventory) bool {
	if !validStandardResourceRead(i.OrgID, i.AppID) || !validStandardResourceRead(i.AppID, i.AccountID) || i.DesiredRevision < 1 || i.DesiredRevision > api.ApplicationStandardMaxVersion || !standardLogInventoryHashValid(i.EffectiveHash) || i.Drains == nil {
		return false
	}
	last := ""
	for _, d := range i.Drains {
		id, err := uuid.Parse(d.DrainID)
		if err != nil || id == uuid.Nil || id.String() != d.DrainID || d.DrainID <= last || !standardLogInventoryHashValid(d.ConfigHash) {
			return false
		}
		last = d.DrainID
	}
	return true
}

func standardLogInventoryHashValid(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == v
}

func sameStandardLogInventory(a, b ApplicationStandardLogInventory) bool {
	return a.OrgID == b.OrgID && a.AppID == b.AppID && a.AccountID == b.AccountID && a.DesiredRevision == b.DesiredRevision && a.EffectiveHash == b.EffectiveHash && slices.Equal(a.Drains, b.Drains)
}

func cloneStandardLogInventory(i ApplicationStandardLogInventory) ApplicationStandardLogInventory {
	i.Drains = append([]ApplicationStandardLogInventoryDrain{}, i.Drains...)
	return i
}

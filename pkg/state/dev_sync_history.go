package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// DevSyncHistory is the durable, intentionally redacted record of one
// successful `gregale dev` edit-to-live cycle. Source paths, environment
// values, credentials, and application logs are never stored here.
type DevSyncHistory struct {
	ID           string
	AppID        string
	DeploymentID string
	Status       string
	EditToLiveMS int64
	SLOTargetMS  int64
	WithinSLO    bool
	Phases       json.RawMessage
	CreatedAt    time.Time
}

// DevSyncHistoryStore is deliberately an optional Store capability. Keeping
// it separate from Store lets older test doubles and rolling-upgrade binaries
// continue to operate while the history table is introduced.
type DevSyncHistoryStore interface {
	RecordDevSyncHistory(context.Context, DevSyncHistory) (DevSyncHistory, error)
	ListDevSyncHistory(context.Context, string, int) ([]DevSyncHistory, error)
}

func validateDevSyncHistory(row DevSyncHistory) error {
	if row.AppID == "" || row.DeploymentID == "" {
		return ErrInvalidArgument
	}
	if row.Status != "live" && row.Status != "failed" {
		return fmt.Errorf("state: invalid developer sync status %q", row.Status)
	}
	if row.EditToLiveMS < 0 || row.EditToLiveMS > 3600000 || row.SLOTargetMS <= 0 || row.SLOTargetMS > 3600000 {
		return fmt.Errorf("state: developer sync timing outside bounds")
	}
	if len(row.Phases) == 0 {
		row.Phases = json.RawMessage(`[]`)
	}
	if !json.Valid(row.Phases) || len(row.Phases) > 8192 {
		return fmt.Errorf("state: invalid developer sync phases")
	}
	return nil
}

func cloneDevSyncHistory(row DevSyncHistory) DevSyncHistory {
	row.Phases = append(json.RawMessage(nil), row.Phases...)
	return row
}

// devSyncHistorySort is shared by both store implementations' in-memory
// projections and keeps newest-first ordering deterministic.
func devSyncHistorySort(rows []DevSyncHistory) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
}

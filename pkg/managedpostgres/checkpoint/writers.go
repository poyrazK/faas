package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// CatalogDatabase is private native catalog identity, including admission and
// template settings. A fingerprint is an observation, never a catalog lock.
type CatalogDatabase struct {
	OID, OwnerOID    uint32
	Name             string
	AllowConnections bool
	IsTemplate       bool
	ConnectionLimit  int32
}

// CatalogFingerprint canonicalizes the entire bounded catalog, including the
// private maintenance database. Database names never leave this digest.
func CatalogFingerprint(databases []CatalogDatabase) (string, error) {
	if len(databases) == 0 || len(databases) > api.PostgresCheckpointDatabasesMax+1 {
		return "", pgerrors.ErrConflict
	}
	rows := slices.Clone(databases)
	slices.SortFunc(rows, func(a, b CatalogDatabase) int {
		if a.OID < b.OID {
			return -1
		}
		if a.OID > b.OID {
			return 1
		}
		return 0
	})
	names := make(map[string]bool, len(rows))
	for i, d := range rows {
		if d.OID == 0 || d.OwnerOID == 0 || d.Name == "" || names[d.Name] || i > 0 && rows[i-1].OID == d.OID {
			return "", pgerrors.ErrConflict
		}
		names[d.Name] = true
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// WriterInventory describes one SQL snapshot. Hidden and unrecognized backend
// types remain unknown; absence cannot prove future writer/catalog isolation.
type WriterInventory struct {
	CatalogFingerprint string `json:"catalog_fingerprint"`
	CatalogComplete    bool   `json:"catalog_complete"`
	DatabaseCount      int    `json:"database_count"`
	ClientSessions     int64  `json:"client_sessions"`
	BackgroundWorkers  int64  `json:"background_workers"`
	ReplicationWorkers int64  `json:"replication_workers"`
	UnknownBackends    int64  `json:"unknown_backends"`
}

func (i WriterInventory) Validate() error {
	if i.DatabaseCount < 1 || i.DatabaseCount > api.PostgresCheckpointDatabasesMax+2 ||
		i.ClientSessions < 0 || i.BackgroundWorkers < 0 || i.ReplicationWorkers < 0 || i.UnknownBackends < 0 {
		return pgerrors.ErrConflict
	}
	if i.CatalogComplete {
		digest, err := hex.DecodeString(i.CatalogFingerprint)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != i.CatalogFingerprint ||
			i.DatabaseCount > api.PostgresCheckpointDatabasesMax+1 {
			return pgerrors.ErrConflict
		}
	} else if i.CatalogFingerprint != "" {
		return pgerrors.ErrConflict
	}
	return nil
}

// Package objectrecovery defines the private, operator-authenticated settlement
// contract. An acknowledgement follows durable gateway settlement; it never
// releases customer capacity or establishes the outcome of an unknown write.
package objectrecovery

import (
	"time"

	"github.com/google/uuid"
)

const Query = "gregale-recovery"

type Settlement struct {
	ID           string `json:"id"`
	Bucket       string `json:"bucket"`
	Key          string `json:"key"`
	Kind         string `json:"kind"`   // put or multipart
	Status       string `json:"status"` // committed, rejected or aborted
	Size         int64  `json:"size"`
	ETag         string `json:"etag"`
	ManifestHash string `json:"manifest_hash"`
}

type Acknowledgement struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	SettledAt time.Time `json:"settled_at"`
}

// NewID preserves canonical UUID wire compatibility while making first use
// expire without keeping a permanent per-request tombstone.
func NewID() string { return uuid.Must(uuid.NewV7()).String() }

func IssuedAt(id string) (time.Time, bool) {
	u, err := uuid.Parse(id)
	if err != nil || u.String() != id || u.Version() != 7 || u.Variant() != uuid.RFC4122 {
		return time.Time{}, false
	}
	seconds, nanos := u.Time().UnixTime()
	return time.Unix(seconds, nanos).UTC(), true
}

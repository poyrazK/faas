package api

import (
	"crypto/rand"
	"encoding/hex"
)

// newUUIDv4 returns an RFC 4122 v4 UUID string. Inline to avoid a uuid
// dependency; spec §4.2 only requires "any v4 UUID" — the shape only
// matters for the server-side Idempotency-Key cache key (apid/server.go).
func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

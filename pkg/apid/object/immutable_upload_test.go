package object

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"strings"
	"testing"
)

func TestVerifiedBackupBodyRequiresExactDigestAndSize(t *testing.T) {
	const content = "PGDMPtest"
	sum := sha256.Sum256([]byte(content))
	for _, tc := range []struct {
		body   string
		sum    hash.Hash
		digest string
		valid  bool
	}{
		{content, sha256.New(), hex.EncodeToString(sum[:]), true},
		{content + "extra", sha256.New(), hex.EncodeToString(sum[:]), false},
		{"PGDMPfake", sha256.New(), hex.EncodeToString(sum[:]), false},
	} {
		reader := &verifiedBackupBody{body: strings.NewReader(tc.body), digest: tc.digest, remaining: int64(len(content)), sum: tc.sum}
		got, err := io.ReadAll(reader)
		if tc.valid && (err != nil || string(got) != content || !reader.verified.Load()) {
			t.Fatalf("valid object: %q %v", got, err)
		}
		if !tc.valid && (!errors.Is(err, errBackupIntegrity) || len(got) >= len(content) || reader.verified.Load()) {
			t.Fatalf("invalid object was streamed completely: %q %v", got, err)
		}
	}
}

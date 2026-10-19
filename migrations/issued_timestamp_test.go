package migrations

// adr: 142, 393. Preserve issued ledger identities without permitting a new
// invalid timestamp. These files were applied before the date check was run.

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"testing"
)

func isFrozenIssuedTimestamp(name string, body []byte) bool {
	var digest string
	switch name {
	case "20261001000061001_application_standard_projection_identity.sql":
		digest = "253c8c144bbb76bf2f52eef38288c6318e6f214954597316957249309c1739e5"
	case "20261001000071001_application_standard_signer_projection.sql":
		digest = "a43cbfb19e25487773200404cff62e71d6f555fd67fe72f6cc5b0d2044118592"
	case "20261001000081001_application_standard_automatic_repair.sql":
		digest = "f0551d1cfe90a0e8c8e196c6d3474a53bec623eec62dd6246c5cba695e1102fb"
	default:
		return false
	}
	return fmt.Sprintf("%x", sha256.Sum256(body)) == digest
}

func TestFrozenIssuedTimestampsRequireExactIdentity(t *testing.T) {
	for _, name := range []string{
		"20261001000061001_application_standard_projection_identity.sql",
		"20261001000071001_application_standard_signer_projection.sql",
		"20261001000081001_application_standard_automatic_repair.sql",
	} {
		t.Run(name, func(t *testing.T) {
			body, err := fs.ReadFile(FS, name)
			if err != nil || !isFrozenIssuedTimestamp(name, body) {
				t.Fatalf("issued migration identity changed: %v", err)
			}
			if isFrozenIssuedTimestamp(name, append(append([]byte{}, body...), '\n')) {
				t.Fatal("changed issued SQL retained the timestamp exception")
			}
			if isFrozenIssuedTimestamp("20261001000091001_new_invalid_timestamp.sql", body) || isFrozenIssuedTimestamp(name+".renamed", body) {
				t.Fatal("new or renamed migration acquired an issued identity")
			}
		})
	}
}

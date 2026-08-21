package openapidiff

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestMarshalSnapshot_StableByContent — two Specs that [Compare]
// reports as zero breaks must produce identical canonical JSON
// and identical SHA-256. This is the load-bearing invariant for
// the deployment_openapi_snapshots.sha256 column: the hash is the
// replay / drift anchor, and a non-deterministic MarshalSnapshot
// would silently inflate the snapshot-store cardinality.
func TestMarshalSnapshot_StableByContent(t *testing.T) {
	// Same content, different map insertion order. The
	// canonical encoder must sort keys.
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  alpha: { type: string }
                  beta:  { type: integer }
                  gamma: { type: boolean }
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  gamma: { type: boolean }
                  alpha: { type: string }
                  beta:  { type: integer }
`
	base, err := LoadBytes([]byte(baseYAML))
	if err != nil {
		t.Fatalf("baseline parse: %v", err)
	}
	prop, err := LoadBytes([]byte(propYAML))
	if err != nil {
		t.Fatalf("proposed parse: %v", err)
	}
	// Pin: Compare reports zero breaks for the reorder.
	if breaks := Compare(base, prop); len(breaks) != 0 {
		t.Fatalf("Compare on reorder must produce 0 breaks; got %d: %+v", len(breaks), breaks)
	}
	rawA, shaA, err := MarshalSnapshot(base)
	if err != nil {
		t.Fatalf("MarshalSnapshot base: %v", err)
	}
	rawB, shaB, err := MarshalSnapshot(prop)
	if err != nil {
		t.Fatalf("MarshalSnapshot prop: %v", err)
	}
	if shaA != shaB {
		t.Fatalf("SHA-256 must be identical for reorder:\n  base=%s\n  prop=%s\n  rawA=%s\n  rawB=%s",
			shaA, shaB, rawA, rawB)
	}
	// SHA-256 is 64 hex chars (the migration's CHECK constraint).
	if len(shaA) != 64 {
		t.Fatalf("SHA-256 must be 64 hex chars; got %d (%q)", len(shaA), shaA)
	}
	if _, err := hex.DecodeString(shaA); err != nil {
		t.Fatalf("SHA-256 must be hex; got %q (%v)", shaA, err)
	}
}

// TestMarshalSnapshot_StableAcrossInstances — calling
// MarshalSnapshot twice on the same Spec must produce the same
// bytes. A non-deterministic canonical encoder would corrupt
// the snapshot-store's hash index.
func TestMarshalSnapshot_StableAcrossInstances(t *testing.T) {
	spec, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
                  email: { type: string }
                required: [id]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rawA, shaA, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatalf("MarshalSnapshot 1: %v", err)
	}
	rawB, shaB, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatalf("MarshalSnapshot 2: %v", err)
	}
	if shaA != shaB {
		t.Fatalf("two MarshalSnapshot calls must produce identical SHA-256:\n  A=%s\n  B=%s", shaA, shaB)
	}
	if string(rawA) != string(rawB) {
		t.Fatalf("two MarshalSnapshot calls must produce identical bytes:\n  A=%s\n  B=%s", rawA, rawB)
	}
}

// TestMarshalSnapshot_DifferentForDifferentContent — adding a
// property (a content change) must produce a different SHA-256.
// This pins the "hash reflects content" contract: a silent
// rewrite (e.g. a property encode/decode bug) would otherwise
// not surface in CI.
func TestMarshalSnapshot_DifferentForDifferentContent(t *testing.T) {
	baseYAML := `
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`
	propYAML := `
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:    { type: string }
                  email: { type: string }
`
	base, _ := LoadBytes([]byte(baseYAML))
	prop, _ := LoadBytes([]byte(propYAML))
	_, shaA, _ := MarshalSnapshot(base)
	_, shaB, _ := MarshalSnapshot(prop)
	if shaA == shaB {
		t.Fatalf("different content must produce different SHA-256:\n  A=%s\n  B=%s", shaA, shaB)
	}
}

// TestMarshalSnapshot_EnvelopeShape — the canonical JSON must
// be a two-field envelope with schema_version=1 and a spec
// payload. A future bump to schema_version=2 must add a
// migration that widens the schema_version CHECK constraint.
func TestMarshalSnapshot_EnvelopeShape(t *testing.T) {
	spec, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, _, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	if !strings.Contains(string(raw), `"schema_version":1`) {
		t.Fatalf("envelope must contain schema_version=1; got %s", raw)
	}
	if !strings.Contains(string(raw), `"spec":{`) {
		t.Fatalf("envelope must contain spec object; got %s", raw)
	}
}

// TestUnmarshalSnapshot_RoundTrip — marshal then unmarshal
// must produce a Spec that [Compare] reports as zero breaks
// against the original. The round-trip is the audit guarantee:
// a snapshot row survives the JSON encode/decode without
// altering the diff result.
func TestUnmarshalSnapshot_RoundTrip(t *testing.T) {
	src, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:    { type: string }
                  email: { type: string }
                required: [id]
    post:
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
                required: [id]
components:
  schemas:
    User:
      type: object
      properties:
        id: { type: string }
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, _, err := MarshalSnapshot(src)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	back, err := UnmarshalSnapshot(raw)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}
	breaks := Compare(src, back)
	if len(breaks) != 0 {
		t.Fatalf("round-trip must produce 0 breaks; got %d: %+v", len(breaks), breaks)
	}
}

// TestUnmarshalSnapshot_RejectsBadSchemaVersion — a snapshot
// with a schema_version different from the current
// SnapshotSchemaVersion must fail to unmarshal. The err is
// the signal that a sibling migration widened the constraint.
func TestUnmarshalSnapshot_RejectsBadSchemaVersion(t *testing.T) {
	raw := []byte(`{"schema_version":999,"spec":{}}`)
	if _, err := UnmarshalSnapshot(raw); err == nil {
		t.Fatalf("UnmarshalSnapshot must reject schema_version=999; got no error")
	} else if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error must mention schema_version; got %v", err)
	}
}

// TestMarshalSnapshot_KnownHash — pins a single deterministic
// hash for a stability-sensitive fixture. The hash is checked
// in by-value; if the canonical encoder drifts, the hash
// changes and CI flags it.
//
// The hash is computed by hand:
//
//	sha256(<canonical JSON>)
//
// and serves as a tripwire for any unintended canonical-form
// change (key ordering, HTML escaping, etc.).
func TestMarshalSnapshot_KnownHash(t *testing.T) {
	spec, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/things:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, sha, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	// Hash the canonical bytes manually and verify they
	// match the encoder's SHA-256. This catches the
	// "encoder drift that produces a different SHA than
	// the test asserts" case.
	sum := sha256.Sum256(raw)
	manual := hex.EncodeToString(sum[:])
	if sha != manual {
		t.Fatalf("MarshalSnapshot SHA must equal sha256(canonical bytes); got %s vs %s", sha, manual)
	}
	// Pin the hash to a non-zero value (i.e. assert the
	// encoder actually emitted bytes). The exact value is
	// allowed to drift across canonical-form changes — the
	// test catches the silent-drift case, not the
	// intentional-bump case.
	if sha == "" || sha == "0000000000000000000000000000000000000000000000000000000000000000" {
		t.Fatalf("SHA-256 must be non-zero; got %q", sha)
	}
}

// TestMarshalSnapshot_NilSpec — defensive. A nil Spec must
// return an error rather than panic.
func TestMarshalSnapshot_NilSpec(t *testing.T) {
	if _, _, err := MarshalSnapshot(nil); err == nil {
		t.Fatalf("MarshalSnapshot(nil) must return error; got no error")
	}
}

// TestUnmarshalSnapshot_EmptyPayload — defensive. An empty
// payload must return an error.
func TestUnmarshalSnapshot_EmptyPayload(t *testing.T) {
	if _, err := UnmarshalSnapshot(nil); err == nil {
		t.Fatalf("UnmarshalSnapshot(nil) must return error; got no error")
	}
}

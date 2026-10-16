package seal

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func str(s string) *string { return &s }

func mustSecret(t *testing.T) (string, Secret) {
	t.Helper()
	text, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSecret(text)
	if err != nil {
		t.Fatal(err)
	}
	return text, s
}

func mustRing(t *testing.T, keys ...Key) *Keyring {
	t.Helper()
	k, err := NewKeyring(keys...)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var now = time.Unix(1_800_000_000, 0)

func sample() Query {
	return Query{
		Database: "db_1",
		SQL:      `SELECT id, body FROM messages WHERE channel_id = $1 AND body <> $2`,
		Params:   []*string{str("42"), nil},
	}
}

// adr: 1384 — a sealed query opens to exactly what was sealed, renewals keep
// the fingerprint, and anything else changes it.
func TestSealRoundTrip(t *testing.T) {
	_, s := mustSecret(t)
	ring := mustRing(t, Key{Database: "db_1", Secret: s})
	token, err := s.Seal(sample(), now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "gls1."+s.KID+".") {
		t.Fatalf("token header: %s", token)
	}
	q, err := ring.Open(token, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want := sample()
	if q.Database != want.Database || q.SQL != want.SQL || len(q.Params) != 2 || *q.Params[0] != "42" || q.Params[1] != nil {
		t.Fatalf("opened %+v", q)
	}
	if q.ExpiresAt() != now.Add(api.LiveQuerySealDefaultTTL) {
		t.Fatalf("expires %s", q.ExpiresAt())
	}
	later, err := s.Seal(sample(), now.Add(time.Minute), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := ring.Open(later, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Fingerprint() != q.Fingerprint() {
		t.Fatal("renewal changed the fingerprint")
	}
	if later == token {
		t.Fatal("two seals share a nonce")
	}
	seen := map[string]bool{q.Fingerprint(): true}
	for _, v := range []Query{
		{Database: "db_2", SQL: want.SQL, Params: want.Params},
		{Database: "db_1", SQL: want.SQL + " ", Params: want.Params},
		{Database: "db_1", SQL: want.SQL, Params: []*string{str("42"), str("")}},
		{Database: "db_1", SQL: want.SQL, Params: []*string{str("42")}},
		{Database: "db_1", SQL: want.SQL, Params: []*string{str("4"), str("2")}},
		{Database: "db_1", SQL: want.SQL, Params: []*string{str("42"), str("N")}},
	} {
		f := v.Fingerprint()
		if seen[f] {
			t.Fatalf("fingerprint collision for %+v", v)
		}
		seen[f] = true
	}
}

// adr: 1384 — Open refuses forged, tampered, retired, expired, oversized and
// cross-database seals.
func TestSealRefusals(t *testing.T) {
	_, a := mustSecret(t)
	_, b := mustSecret(t)
	_, other := mustSecret(t)
	ring := mustRing(t,
		Key{Database: "db_1", Secret: a},
		Key{Database: "db_2", Secret: b},
		Key{Database: "db_1", Secret: other, Retires: now.Add(time.Minute)},
	)
	good, err := a.Seal(sample(), now, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.SplitN(good, ".", 3)[2]
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	flipped := append([]byte(nil), raw...)
	flipped[len(flipped)-1] ^= 1
	fromB, err := b.Seal(sample(), now, 0) // db_1 query under db_2's key
	if err != nil {
		t.Fatal(err)
	}
	fromRetiring, err := other.Seal(sample(), now, 0)
	if err != nil {
		t.Fatal(err)
	}
	future, err := a.Seal(sample(), now.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	long, err := a.seal(Query{Database: "db_1", SQL: "SELECT 1", Params: []*string{}, IssuedAt: now.Unix(), Expires: now.Unix() + 301}, make([]byte, nonceLength))
	if err != nil {
		t.Fatal(err)
	}
	extra := sealPlain(t, a, `{"db":"db_1","sql":"SELECT 1","params":[],"iat":1800000000,"exp":1800000060,"role":"postgres"}`)
	_, unknown := mustSecret(t)
	stranger, err := unknown.Seal(sample(), now, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		token string
		at    time.Time
		want  error
	}{
		{"valid within skew", good, now.Add(api.LiveQuerySealDefaultTTL + api.LiveQuerySealClockSkew), nil},
		{"expired", good, now.Add(api.LiveQuerySealDefaultTTL + api.LiveQuerySealClockSkew + time.Second), ErrExpired},
		{"tampered body", "gls1." + a.KID + "." + base64.RawURLEncoding.EncodeToString(flipped), now, ErrForged},
		{"header swapped to another key", "gls1." + b.KID + "." + body, now, ErrForged},
		{"wrong database", fromB, now, ErrDatabase},
		{"retiring key before retirement", fromRetiring, now.Add(59 * time.Second), nil},
		{"retired key", fromRetiring, now.Add(time.Minute), ErrUnknownKey},
		{"unknown key", stranger, now, ErrUnknownKey},
		{"issued in the future", future, now, ErrInvalid},
		{"lifetime over the maximum", long, now, ErrInvalid},
		{"unknown payload field", extra, now, ErrInvalid},
		{"no body", "gls1." + a.KID, now, ErrMalformed},
		{"bad prefix", "gls2." + a.KID + "." + body, now, ErrMalformed},
		{"bad kid", "gls1.ABCDEF0123456789." + body, now, ErrMalformed},
		{"bad base64", "gls1." + a.KID + ".***", now, ErrMalformed},
		{"short body", "gls1." + a.KID + ".AAAA", now, ErrMalformed},
		{"oversized", "gls1." + a.KID + "." + strings.Repeat("A", 80<<10), now, ErrMalformed},
	} {
		_, err := ring.Open(tc.token, tc.at)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := NewKeyring(Key{Database: "db_1", Secret: a}, Key{Database: "db_2", Secret: a}); err == nil {
		t.Fatal("duplicate key IDs accepted")
	}
}

func sealPlain(t *testing.T, s Secret, plain string) string {
	t.Helper()
	nonce := make([]byte, nonceLength)
	header := tokenPrefix + "." + s.KID
	return header + "." + base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(plain), []byte(header)))
}

// adr: 1384 — Seal enforces the ADR's SQL, parameter and lifetime limits.
func TestSealLimits(t *testing.T) {
	_, s := mustSecret(t)
	many := make([]*string, api.LiveQueryParamsMax+1)
	for _, tc := range []struct {
		name string
		q    Query
		ttl  time.Duration
		want error
	}{
		{"max lifetime", sample(), api.LiveQuerySealMaxTTL, nil},
		{"lifetime over the maximum", sample(), api.LiveQuerySealMaxTTL + time.Second, ErrInvalid},
		{"no database", Query{SQL: "SELECT 1"}, 0, ErrInvalid},
		{"blank SQL", Query{Database: "db_1", SQL: "  "}, 0, ErrInvalid},
		{"SQL at the limit", Query{Database: "db_1", SQL: "SELECT 1" + strings.Repeat(" ", api.LiveQuerySQLMaxBytes-8)}, 0, nil},
		{"SQL over the limit", Query{Database: "db_1", SQL: "SELECT 1" + strings.Repeat(" ", api.LiveQuerySQLMaxBytes-7)}, 0, ErrInvalid},
		{"too many parameters", Query{Database: "db_1", SQL: "SELECT 1", Params: many}, 0, ErrInvalid},
		{"parameter over the limit", Query{Database: "db_1", SQL: "SELECT $1", Params: []*string{str(strings.Repeat("x", api.LiveQueryParamMaxBytes+1))}}, 0, ErrInvalid},
		{"NUL in a parameter", Query{Database: "db_1", SQL: "SELECT $1", Params: []*string{str("a\x00b")}}, 0, ErrInvalid},
		{"invalid UTF-8", Query{Database: "db_1", SQL: "SELECT '\xff'"}, 0, ErrInvalid},
		{"payload over the limit", Query{Database: "db_1", SQL: "SELECT 1", Params: []*string{
			str(strings.Repeat("x", 16<<10)), str(strings.Repeat("x", 16<<10)), str(strings.Repeat("x", 16<<10)),
		}}, 0, ErrInvalid},
	} {
		_, err := s.Seal(tc.q, now, tc.ttl)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	for _, bad := range []string{"", "glsk1_0123456789abcdef", "glsk2_0123456789abcdef_" + strings.Repeat("A", 43), "glsk1_0123456789abcdef_AAAA"} {
		if _, err := ParseSecret(bad); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseSecret(%q) = %v", bad, err)
		}
	}
	codes := map[error]string{ErrExpired: "seal_expired", ErrInvalid: "seal_invalid", ErrDatabase: "forbidden", ErrForged: "seal_rejected", ErrUnknownKey: "seal_rejected", ErrMalformed: "seal_rejected"}
	for err, code := range codes {
		if got := Code(err); got != code {
			t.Errorf("Code(%v) = %s, want %s", err, got, code)
		}
	}
}

// vector is the known-answer test the SDKs share: Query serializes to
// Plaintext exactly (JSON.stringify's output), and sealing it under Secret
// with Nonce produces Token exactly.
type vector struct {
	Secret    string `json:"secret"`
	Nonce     string `json:"nonce"`
	Query     Query  `json:"query"`
	Plaintext string `json:"plaintext"`
	Token     string `json:"token"`
}

// adr: 1384 — the seal format is pinned by a known-answer vector so the
// JavaScript SDK and realtimed cannot drift apart.
func TestSealVector(t *testing.T) {
	material := make([]byte, keyLength)
	nonce := make([]byte, nonceLength)
	for i := range material {
		material[i] = byte(i)
	}
	for i := range nonce {
		nonce[i] = byte(0xa0 + i)
	}
	v := vector{
		Secret: "glsk1_0123456789abcdef_" + base64.RawURLEncoding.EncodeToString(material),
		Nonce:  base64.RawURLEncoding.EncodeToString(nonce),
		Query: Query{
			Database: "db_7f3a",
			SQL:      `SELECT id, body FROM messages WHERE channel_id = $1 AND body <> $2 AND tag = $3`,
			Params:   []*string{str("42"), nil, str(`<"ünïcode">`)},
			IssuedAt: 1_800_000_000,
			Expires:  1_800_000_060,
		},
	}
	s, err := ParseSecret(v.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if v.Token, err = s.seal(v.Query, nonce); err != nil {
		t.Fatal(err)
	}
	sealed, _ := base64.RawURLEncoding.DecodeString(strings.SplitN(v.Token, ".", 3)[2])
	plain, err := s.aead.Open(nil, nonce, sealed[nonceLength:], []byte("gls1."+s.KID))
	if err != nil {
		t.Fatal(err)
	}
	v.Plaintext = string(plain)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	const path = "testdata/vector.json"
	if os.Getenv("LIVEQUERY_UPDATE_TESTDATA") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("seal vector changed; the SDKs depend on it:\n%s", got)
	}
	ring := mustRing(t, Key{Database: "db_7f3a", Secret: s})
	q, err := ring.Open(v.Token, time.Unix(1_800_000_010, 0))
	if err != nil {
		t.Fatal(err)
	}
	if q.Fingerprint() != v.Query.Fingerprint() {
		t.Fatal("vector does not open to its query")
	}
}

// adr:375
package copyinventory

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestCopyDatabaseCatalogueLegacyFingerprintAndDetachedConfig(t *testing.T) {
	f := newInventoryFixture(t)
	i, err := Read(t.Context(), f.conn, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Freeze the exact old v1 database field ordering and explicit null encoding.
	// A missing optional ICU rule must not change a previously retained MAC.
	type legacyDatabase struct {
		OID              uint32   `json:"oid"`
		Name             string   `json:"name"`
		OwnerOID         uint32   `json:"owner_oid"`
		Owner            string   `json:"owner"`
		Encoding         int32    `json:"encoding"`
		Template         bool     `json:"template"`
		AllowConnections bool     `json:"allow_connections"`
		ConnectionLimit  int32    `json:"connection_limit"`
		TablespaceOID    uint32   `json:"tablespace_oid"`
		Collation        string   `json:"collation"`
		CType            string   `json:"ctype"`
		ACL              []string `json:"acl"`
		LocaleProvider   *string  `json:"locale_provider"`
		Locale           *string  `json:"locale"`
		CollationVersion *string  `json:"collation_version"`
	}
	old := struct {
		Version              int                   `json:"version"`
		PostgresMajor        int                   `json:"postgres_major"`
		DatabaseOID          uint32                `json:"database_oid"`
		RoleOID              uint32                `json:"role_oid"`
		Databases            []legacyDatabase      `json:"databases"`
		Roles                []Role                `json:"roles"`
		Memberships          []Membership          `json:"memberships"`
		Settings             []Setting             `json:"settings"`
		Tablespaces          []Tablespace          `json:"tablespaces"`
		PreparedTransactions []PreparedTransaction `json:"prepared_transactions"`
	}{Version: i.body.Version, PostgresMajor: i.body.PostgresMajor, DatabaseOID: i.body.DatabaseOID, RoleOID: i.body.RoleOID, Roles: i.body.Roles, Memberships: i.body.Memberships, Settings: i.body.Settings, Tablespaces: i.body.Tablespaces, PreparedTransactions: i.body.PreparedTransactions}
	for _, d := range i.body.Databases {
		if d.ICURules != nil {
			t.Fatal("legacy fixture requires absence of ICU rules")
		}
		old.Databases = append(old.Databases, legacyDatabase{d.OID, d.Name, d.OwnerOID, d.Owner, d.Encoding, d.Template, d.AllowConnections, d.ConnectionLimit, d.TablespaceOID, d.Collation, d.CType, d.ACL, d.LocaleProvider, d.Locale, d.CollationVersion})
	}
	legacy, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	current, err := i.PayloadForSealing()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy, current) {
		t.Fatal("legacy v1 canonical payload changed")
	}
	m := hmac.New(sha256.New, f.cfg.FingerprintKey[:])
	_, _ = m.Write([]byte("gregale.snapshot-copy.cluster-inventory.v1\x00"))
	_, _ = m.Write(legacy)
	fp := hex.EncodeToString(m.Sum(nil))
	if _, err = RecoverPrivatePayload(legacy, f.cfg, fp); err != nil || fp != i.fingerprint {
		t.Fatal("legacy retained MAC no longer recovers", err)
	}
	c, err := i.DatabaseCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	c.Databases[0].Name = "changed"
	c.Settings[0].Config[0] = "changed"
	c.Tablespaces[0].Name = "changed"
	after, _ := i.PayloadForSealing()
	if !bytes.Equal(current, after) {
		t.Fatal("worker catalogue aliases immutable inventory")
	}
}

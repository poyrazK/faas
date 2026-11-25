package dbmigration

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	Verifying                = "verifying"
	Verified                 = "verified"
	MaxVerificationSequences = 64
)

// Verification is evidence for one operator-declared write pause.
type Verification struct {
	ID              string    `json:"verification_id"`
	State           string    `json:"state"`
	Reason          string    `json:"reason,omitempty"`
	AppliedLSN      string    `json:"applied_lsn,omitempty"`
	SequencesSynced int       `json:"sequences_synced"`
	VerifiedAt      time.Time `json:"verified_at,omitzero"`
}

// Marker gives each migration an isolated table in the existing publication,
// so observing its row proves progress on the same subscriber as the data.
type Marker struct {
	Name string
	Tag  string
}

func VerificationMarker(resource, operation string) Marker {
	return Marker{Name: resource + "_barrier", Tag: "gregale:migration:" + operation + ":" + resource}
}

func (m Marker) table() string { return "public." + Ident(m.Name) }

func (m Marker) CreateSQL(publication string) string {
	sql := "CREATE TABLE " + m.table() + " (token uuid PRIMARY KEY); COMMENT ON TABLE " +
		m.table() + " IS " + Literal(m.Tag)
	if publication != "" {
		sql += "; ALTER PUBLICATION " + Ident(publication) + " ADD TABLE ONLY " + m.table()
	}
	return sql
}

// StateSQL returns null when absent, true for our exact object and false
// for a conflicting name. A lookalike must never be reused or dropped.
func (m Marker) StateSQL() string { return m.stateSQL(" AND c.relacl IS NULL") }

// TargetStateSQL permits staged grants on the fenced target marker. The
// activation moves this table into a private schema before those roles connect.
func (m Marker) TargetStateSQL() string { return m.stateSQL("") }

func (m Marker) stateSQL(acl string) string {
	return `SELECT coalesce((SELECT (c.relkind='r' AND c.relpersistence='p'
 AND c.relowner=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user)
 AND coalesce(obj_description(c.oid,'pg_class'),'')=` + Literal(m.Tag) + `
 AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND c.relreplident='d'
 AND NOT c.relhassubclass` + acl + `
 AND NOT EXISTS (SELECT FROM pg_catalog.pg_inherits WHERE inhrelid=c.oid)
 AND NOT EXISTS (SELECT FROM pg_catalog.pg_trigger WHERE tgrelid=c.oid)
 AND NOT EXISTS (SELECT FROM pg_catalog.pg_rewrite WHERE ev_class=c.oid)
 AND (SELECT count(*) FROM pg_catalog.pg_attribute WHERE attrelid=c.oid AND attnum>0)=1
 AND EXISTS (SELECT FROM pg_catalog.pg_attribute WHERE attrelid=c.oid AND attnum=1
   AND attname='token' AND atttypid='pg_catalog.uuid'::regtype AND attnotnull
   AND NOT atthasdef AND attidentity='' AND attgenerated='' AND NOT attisdropped)
 AND EXISTS (SELECT FROM pg_catalog.pg_index WHERE indrelid=c.oid AND indisprimary
   AND indisvalid AND indimmediate AND indkey::text='1'))::text
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relname=` + Literal(m.Name) + `),'null')`
}

func (m Marker) InsertSQL(token string) string {
	value := Literal(token) + "::uuid"
	return "SET LOCAL synchronous_commit=on; DELETE FROM " + m.table() + " WHERE token <> " + value +
		"; INSERT INTO " + m.table() + " (token) VALUES (" + value + ") ON CONFLICT DO NOTHING"
}

func (m Marker) SeenSQL(token string) string {
	return "SELECT EXISTS (SELECT FROM " + m.table() + " WHERE token=" + Literal(token) + "::uuid)"
}

func (m Marker) DropSQL() string { return "DROP TABLE " + m.table() }

const SequenceInventorySQL = `
SELECT coalesce(json_agg(row_to_json(seq) ORDER BY seq.schema COLLATE "C", seq.name COLLATE "C"),'[]'::json)
FROM (
 SELECT n.nspname::text AS schema,c.relname::text AS name,
    s.seqtypid::regtype::text AS type,s.seqstart AS start,s.seqincrement AS increment,
    s.seqmin AS minimum,s.seqmax AS maximum,s.seqcache AS cache,s.seqcycle AS cycle,
    c.relpersistence='p'
    AND NOT EXISTS (SELECT FROM pg_catalog.pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid
      AND d.deptype IN ('a','i','e') AND d.objsubid=0) AS standalone
 FROM pg_catalog.pg_sequence s JOIN pg_catalog.pg_class c ON c.oid=s.seqrelid
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
 ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C" LIMIT 65
) seq`

type Sequence struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Start      int64  `json:"start"`
	Increment  int64  `json:"increment"`
	Minimum    int64  `json:"minimum"`
	Maximum    int64  `json:"maximum"`
	Cache      int64  `json:"cache"`
	Cycle      bool   `json:"cycle"`
	Standalone bool   `json:"standalone"`
}

type SequenceValue struct {
	Name   string `json:"name"`
	Last   int64  `json:"last_value"`
	Called bool   `json:"is_called"`
}

func (s Sequence) relation() string { return "public." + Ident(s.Name) }

func (s Sequence) Valid() bool {
	return s.Schema == "public" && safePreparationName(s.Name) && s.Standalone &&
		(s.Type == "smallint" || s.Type == "integer" || s.Type == "bigint") &&
		s.Minimum < s.Maximum && s.Start >= s.Minimum && s.Start <= s.Maximum && s.Increment != 0 && s.Cache > 0
}

func (s Sequence) CreateSQL() (string, error) {
	if !s.Valid() {
		return "", errors.New("unsupported migration sequence")
	}
	cycle := "NO CYCLE"
	if s.Cycle {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("CREATE SEQUENCE %s AS %s INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s",
		s.relation(), s.Type, s.Increment, s.Minimum, s.Maximum, s.Start, s.Cache, cycle), nil
}

func SequenceValuesSQL(sequences []Sequence) (string, error) {
	if len(sequences) == 0 {
		return "SELECT '[]'::json", nil
	}
	if len(sequences) > MaxVerificationSequences {
		return "", errors.New("too many migration sequences")
	}
	rows := make([]string, 0, len(sequences))
	for _, s := range sequences {
		if !s.Valid() {
			return "", errors.New("unsupported migration sequence")
		}
		rows = append(rows, "SELECT "+Literal(s.Name)+" AS name, last_value, is_called FROM "+s.relation())
	}
	return "SELECT json_agg(row_to_json(seq) ORDER BY name COLLATE \"C\") FROM (" + strings.Join(rows, " UNION ALL ") + ") seq", nil
}

func (s Sequence) SetValueSQL(v SequenceValue) (string, error) {
	if !s.Valid() || v.Name != s.Name || v.Last < s.Minimum || v.Last > s.Maximum {
		return "", errors.New("invalid migration sequence value")
	}
	return fmt.Sprintf("SELECT pg_catalog.setval(%s::regclass,%d,%t)", Literal(s.relation()), v.Last, v.Called), nil
}

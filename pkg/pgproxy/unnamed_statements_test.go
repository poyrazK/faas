// adr: 1027
package pgproxy

import (
	"bytes"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
)

func TestSkippedUnnamedStatementChangesAreRolledBack(t *testing.T) {
	s := &pooledSession{statements: map[string]*pgproto3.Parse{}, server: &serverConn{prepared: map[string]bool{}, fe: pgproto3.NewFrontend(bytes.NewReader(nil), &bytes.Buffer{})}}
	if err := s.forward(&pgproto3.Parse{Name: "", Query: "SELECT 11"}); err != nil {
		t.Fatal(err)
	}
	s.pop('P')                                          // the first Parse succeeded
	s.expect = append(s.expect, expectation{kind: 'E'}) // this Execute will fail
	if err := s.forward(&pgproto3.Parse{Name: "", Query: "SELECT 22"}); err != nil {
		t.Fatal(err)
	}
	if err := s.forward(&pgproto3.Close{ObjectType: 'S', Name: ""}); err != nil {
		t.Fatal(err)
	}
	s.expect = append(s.expect, expectation{kind: 'S'})
	s.skipToSync()
	if p := s.statements[""]; p == nil || p.Query != "SELECT 11" {
		t.Fatalf("ignored Parse/Close replaced the earlier statement: %+v", p)
	}
	if len(s.expect) != 1 || s.expect[0].kind != 'S' {
		t.Fatalf("did not stop skipping at Sync: %+v", s.expect)
	}
}

func TestFailedParseRestoresNamedAndInvalidatesUnnamed(t *testing.T) {
	for _, name := range []string{"", "named"} {
		t.Run(map[bool]string{true: "unnamed", false: "named"}[name == ""], func(t *testing.T) {
			s := &pooledSession{statements: map[string]*pgproto3.Parse{}, server: &serverConn{prepared: map[string]bool{}, fe: pgproto3.NewFrontend(bytes.NewReader(nil), &bytes.Buffer{})}}
			if err := s.forward(&pgproto3.Parse{Name: name, Query: "SELECT 11"}); err != nil {
				t.Fatal(err)
			}
			s.pop('P')
			if err := s.forward(&pgproto3.Parse{Name: name, Query: "SELECT FROM"}); err != nil {
				t.Fatal(err)
			}
			s.expect = append(s.expect, expectation{kind: 'S'})
			s.skipToSync()
			got := s.statements[name]
			if name == "" {
				if got != nil {
					t.Fatal("failed unnamed Parse retained the old statement")
				}
			} else if got == nil || got.Query != "SELECT 11" {
				t.Fatalf("failed named Parse replaced the old statement: %+v", got)
			}
		})
	}
}

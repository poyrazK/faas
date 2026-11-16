package livequery

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestDAPI014DomainArraysUseRecursiveBaseWireTypes(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `CREATE DOMAIN cents AS numeric(30,12);CREATE DOMAIN flag AS boolean;CREATE DOMAIN document AS jsonb;CREATE DOMAIN amounts AS cents[];CREATE TYPE "_enum_label" AS ENUM('one','two');CREATE DOMAIN label AS "_enum_label"`)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := conn.Conn().Prepare(ctx, "", `SELECT ARRAY[1.234567890123::cents,NULL] AS amounts,ARRAY[true::flag,NULL] AS flags,ARRAY['{"n":9007199254740993}'::document,NULL] AS documents,NULL::amounts AS domain_array, ARRAY['one'::label,NULL] AS enum_domain`)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := resultColumns(ctx, conn.Conn(), desc.Fields)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"_numeric", "_bool", "_jsonb", "_numeric", "_text"} {
		if columns[i].Type != want {
			t.Errorf("column %d type %q, want %q", i, columns[i].Type, want)
		}
	}
}

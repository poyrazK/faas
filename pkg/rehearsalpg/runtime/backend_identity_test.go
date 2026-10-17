package runtime

import (
	"strings"
	"testing"
)

func TestPostgresRealCleanupCannotAcknowledgeAnotherBackend(t *testing.T) {
	p, s := newPostgresTest(t)
	r, writer := createPostgresTest(t, s)
	resource, err := s.Catalog.ScenarioPostgresForApp(t.Context(), r.AppID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := &Postgres{config: p.config.Copy(), identity: strings.Repeat("0", 64)}
	if foreign.Drop(t.Context(), resource) == nil {
		t.Fatal("wrong backend accepted a cleanup journal")
	}
	resource.BackendSHA256 = foreign.identity
	if foreign.Drop(t.Context(), resource) == nil {
		t.Fatal("replaced cluster produced a false deletion receipt")
	}
	if _, err = writer.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatal("rejected cleanup affected the fixture")
	}
}

package sealdomain_test

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestRegistry_EveryColumnHasOneDomain(t *testing.T) {
	seen := map[string]sealdomain.Domain{}
	for _, s := range sealdomain.All() {
		if len(s.Readers) == 0 || len(s.Roles) == 0 || len(s.Columns) == 0 {
			t.Errorf("%s: readers, roles and columns are required", s.Domain)
		}
		if _, err := sealdomain.Parse(string(s.Domain)); err != nil {
			t.Errorf("Parse(%q): %v", s.Domain, err)
		}
		for _, c := range s.Columns {
			if prev, dup := seen[c.String()]; dup {
				t.Errorf("%s is registered to %s and %s", c, prev, s.Domain)
			}
			seen[c.String()] = s.Domain
			if got, ok := sealdomain.ColumnDomain(c.Table, c.Name); !ok || got != s.Domain {
				t.Errorf("ColumnDomain(%s) = %q, %v; want %q", c, got, ok, s.Domain)
			}
		}
	}
	for _, c := range sealdomain.ProbeColumns {
		if d, ok := seen[c.String()]; ok {
			t.Errorf("probe column %s is also registered to %s", c, d)
		}
	}
}

func TestRegistry_OutboundIsWiredToOutbounddAlone(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Outbound)
	if !ok || !s.Wired {
		t.Fatalf("outbound domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 1 || s.Readers[0] != "outboundd" {
		t.Fatalf("outbound readers = %v, want [outboundd]", s.Readers)
	}
	if got := sealdomain.ReaderDomains("apid"); containsDomain(got, sealdomain.Outbound) {
		t.Fatalf("apid reads %v; it must only seal outbound credentials", got)
	}
}

func TestRegistry_SourceIsWiredToGithubdAlone(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Source)
	if !ok || !s.Wired {
		t.Fatalf("source domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 1 || s.Readers[0] != "githubd" {
		t.Fatalf("source readers = %v, want [githubd]", s.Readers)
	}
	if got := sealdomain.ReaderDomains("apid"); containsDomain(got, sealdomain.Source) {
		t.Fatalf("apid reads %v; it must only seal webhook secrets", got)
	}
}

func TestRegistry_ControlIsWiredToScheddAlone(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Control)
	if !ok || !s.Wired {
		t.Fatalf("control domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 1 || s.Readers[0] != "schedd" {
		t.Fatalf("control readers = %v, want [schedd]", s.Readers)
	}
	if len(s.Roles) != 2 {
		t.Fatalf("control roles = %v; schedd runs on both node roles", s.Roles)
	}
}

func TestRegistry_DeliveryIsWiredToScheddAlone(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Delivery)
	if !ok || !s.Wired {
		t.Fatalf("delivery domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 1 || s.Readers[0] != "schedd" {
		t.Fatalf("delivery readers = %v, want [schedd]", s.Readers)
	}
	if len(s.Roles) != 2 {
		t.Fatalf("delivery roles = %v; schedd runs on both node roles", s.Roles)
	}
	// apid both seals and opens a managed workflow's plan, so the plan
	// belongs to apid's domain, not to schedd's.
	for _, c := range s.Columns {
		if c.Table == "agent_execution_workflows" {
			t.Fatalf("delivery owns %s, which only apid opens", c)
		}
	}
	if got := sealdomain.ReaderDomains("apid"); containsDomain(got, sealdomain.Delivery) {
		t.Fatalf("apid reads %v; it must only seal delivery values", got)
	}
}

func TestRegistry_AccountIsWiredToApidOnTheControlPlane(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Account)
	if !ok || !s.Wired {
		t.Fatalf("account domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Roles) != 1 || s.Roles[0] != sealdomain.RoleControlPlane {
		t.Fatalf("account roles = %v; a compute node never holds account material", s.Roles)
	}
	if got := sealdomain.ReaderDomains("apid"); !containsDomain(got, sealdomain.Account) {
		t.Fatalf("apid reads %v; it opens account values", got)
	}
	if got := sealdomain.ReaderDomains("schedd"); containsDomain(got, sealdomain.Account) {
		t.Fatalf("schedd reads %v; it must not open account values", got)
	}
}

func TestRegistry_StorageIsWiredToS3GatewaydAlone(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Storage)
	if !ok || !s.Wired {
		t.Fatalf("storage domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 1 || s.Readers[0] != "s3-gatewayd" {
		t.Fatalf("storage readers = %v, want [s3-gatewayd]", s.Readers)
	}
	if containsDomain(sealdomain.ReaderDomains("apid"), sealdomain.Storage) {
		t.Fatal("apid reads the storage domain; it must only seal S3 credentials")
	}
}

func TestKeyID(t *testing.T) {
	newRecipient := func() *age.X25519Recipient {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id.Recipient()
	}
	domain, fleet := newRecipient(), newRecipient()
	for name, tc := range map[string]struct {
		recipients []*age.X25519Recipient
		want       string
	}{
		"fleet alone":        {[]*age.X25519Recipient{fleet}, fleet.String()},
		"dual keeps fleet":   {[]*age.X25519Recipient{domain, fleet}, fleet.String()},
		"domain alone":       {[]*age.X25519Recipient{domain}, domain.String()},
		"nothing to seal to": {nil, ""},
	} {
		if got := sealdomain.KeyID(tc.recipients, fleet); got != tc.want {
			t.Errorf("%s: KeyID = %q, want %q", name, got, tc.want)
		}
	}
}

func TestRegistry_AlertingIsWiredToMeterdAlone(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Alerting)
	if !ok || !s.Wired {
		t.Fatalf("alerting domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 1 || s.Readers[0] != "meterd" {
		t.Fatalf("alerting readers = %v, want [meterd]", s.Readers)
	}
	// apid seals alerting values and sends the tests it cannot open
	// through meterd (ADR 20261022111812, section 3).
	if containsDomain(sealdomain.ReaderDomains("apid"), sealdomain.Alerting) {
		t.Fatal("apid reads the alerting domain; it must only seal alerting values")
	}
}

func TestRegistry_EdgeIsWiredToGatewaydInternalAlone(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Edge)
	if !ok || !s.Wired {
		t.Fatalf("edge domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 1 || s.Readers[0] != "gatewayd-internal" {
		t.Fatalf("edge readers = %v, want [gatewayd-internal]", s.Readers)
	}
	for _, d := range []string{"apid", "gatewayd-public"} {
		if containsDomain(sealdomain.ReaderDomains(d), sealdomain.Edge) {
			t.Fatalf("%s reads the edge domain; only gatewayd-internal opens its columns", d)
		}
	}
}

// imaged opens registry passwords to pull images; vmmd opens them into a
// builder VM's Docker config for its base-image pulls (ADR-20261026090000).
// Both run on compute nodes, where root already holds the build identity.
func TestRegistry_BuildIsWiredToImagedAndVmmd(t *testing.T) {
	s, ok := sealdomain.Lookup(sealdomain.Build)
	if !ok || !s.Wired {
		t.Fatalf("build domain = %+v, %v; want wired", s, ok)
	}
	if len(s.Readers) != 2 || s.Readers[0] != "imaged" || s.Readers[1] != "vmmd" {
		t.Fatalf("build readers = %v, want [imaged vmmd]", s.Readers)
	}
	if got := sealdomain.ReaderDomains("apid"); containsDomain(got, sealdomain.Build) {
		t.Fatalf("apid reads %v; it must only seal registry passwords", got)
	}
}

func TestParse_RejectsUnknownDomain(t *testing.T) {
	if _, err := sealdomain.Parse("fleet"); err == nil {
		t.Fatal("Parse accepted a name outside the registry")
	}
}

func TestRecipients(t *testing.T) {
	domain := mustIdentity(t).Recipient()
	fleet := mustIdentity(t).Recipient()
	for _, tc := range []struct {
		name    string
		mode    sealdomain.WriteMode
		domain  *age.X25519Recipient
		fleet   *age.X25519Recipient
		want    []*age.X25519Recipient
		wantErr bool
	}{
		{"auto without the domain seals to fleet", sealdomain.WriteAuto, nil, fleet, []*age.X25519Recipient{fleet}, false},
		{"auto with the domain seals to both", sealdomain.WriteAuto, domain, fleet, []*age.X25519Recipient{domain, fleet}, false},
		{"auto with nothing staged fails", sealdomain.WriteAuto, nil, nil, nil, true},
		{"fleet ignores a staged domain", sealdomain.WriteFleet, domain, fleet, []*age.X25519Recipient{fleet}, false},
		{"dual seals to both", sealdomain.WriteDual, domain, fleet, []*age.X25519Recipient{domain, fleet}, false},
		{"dual without the domain fails", sealdomain.WriteDual, nil, fleet, nil, true},
		{"domain seals to the domain alone", sealdomain.WriteDomain, domain, fleet, []*age.X25519Recipient{domain}, false},
		{"domain without the domain fails", sealdomain.WriteDomain, nil, fleet, nil, true},
		{"domain needs no fleet", sealdomain.WriteDomain, domain, nil, []*age.X25519Recipient{domain}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sealdomain.Recipients(tc.mode, tc.domain, tc.fleet)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Recipients = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Recipients = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i].String() != tc.want[i].String() {
					t.Fatalf("Recipients[%d] = %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseWriteMode(t *testing.T) {
	for in, want := range map[string]sealdomain.WriteMode{"": sealdomain.WriteAuto, "fleet": sealdomain.WriteFleet, " dual ": sealdomain.WriteDual, "domain": sealdomain.WriteDomain} {
		got, err := sealdomain.ParseWriteMode(in)
		if err != nil || got != want {
			t.Errorf("ParseWriteMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := sealdomain.ParseWriteMode("both"); err == nil {
		t.Error("ParseWriteMode accepted an unknown mode")
	}
}

func TestLoadReaderIdentities(t *testing.T) {
	dir := t.TempDir()
	domain := writeIdentity(t, filepath.Join(dir, "faas_seal_domain_outbound"))
	domainPrev := writeIdentity(t, filepath.Join(dir, "faas_seal_domain_outbound_previous"))
	fleet := writeIdentity(t, filepath.Join(dir, "fleet.age"))
	fleetPrev := writeIdentity(t, filepath.Join(dir, "fleet.age.previous"))

	got, err := sealdomain.LoadReaderIdentities(filepath.Join(dir, "faas_seal_domain_outbound"), filepath.Join(dir, "fleet.age"), "", filepath.Join(dir, "fleet.age"))
	if err != nil {
		t.Fatal(err)
	}
	want := []*age.X25519Identity{domain, domainPrev, fleet, fleetPrev}
	if len(got) != len(want) {
		t.Fatalf("loaded %d identities, want %d (domain, its previous, fleet, its previous; duplicates dropped)", len(got), len(want))
	}
	for i := range want {
		if got[i].Recipient().String() != want[i].Recipient().String() {
			t.Fatalf("identity %d out of order", i)
		}
	}

	if _, err := sealdomain.LoadReaderIdentities(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("LoadReaderIdentities succeeded with no identity on disk")
	}
	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, []byte(mustIdentity(t).String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sealdomain.LoadReaderIdentities(loose); err == nil {
		t.Fatal("LoadReaderIdentities loaded a world-readable identity")
	}
}

func TestProbe(t *testing.T) {
	domain := mustIdentity(t)
	fleet := mustIdentity(t)
	blob, err := sealdomain.SealProbe(sealdomain.Outbound, domain.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := sealdomain.OpenProbe(sealdomain.Outbound, []*age.X25519Identity{domain}, blob); err != nil {
		t.Fatalf("domain identity: %v", err)
	}
	if err := sealdomain.OpenProbe(sealdomain.Outbound, []*age.X25519Identity{fleet}, blob); err == nil {
		t.Fatal("the fleet identity opened a domain probe")
	}
	if err := sealdomain.OpenProbe(sealdomain.Runtime, []*age.X25519Identity{domain}, blob); err == nil {
		t.Fatal("the outbound probe verified as the runtime probe")
	}
}

func TestGithubWebhookSecretRoundTrip(t *testing.T) {
	source := mustIdentity(t)
	fleet := mustIdentity(t)
	secret := []byte("0123456789abcdef0123456789abcdef")
	sealed, err := sealdomain.SealGithubWebhookSecret([]*age.X25519Recipient{fleet.Recipient()}, secret)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{source, fleet}, sealed)
	if err != nil || string(got) != string(secret) {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	other, err := secretbox.SealBytes(fleet.Recipient(), "outbound_authorization", secret, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{fleet}, other); err == nil {
		t.Fatal("an envelope of another namespace opened as a webhook secret")
	}
	if _, err := sealdomain.SealGithubWebhookSecret([]*age.X25519Recipient{fleet.Recipient()}, make([]byte, sealdomain.GithubWebhookMaxBytes+1)); err == nil {
		t.Fatal("sealed a webhook secret over GitHub's 64-byte key limit")
	}
}

func containsDomain(ds []sealdomain.Domain, want sealdomain.Domain) bool {
	for _, d := range ds {
		if d == want {
			return true
		}
	}
	return false
}

func mustIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func writeIdentity(t *testing.T, path string) *age.X25519Identity {
	t.Helper()
	id := mustIdentity(t)
	if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	return id
}

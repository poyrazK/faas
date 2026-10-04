// adr:567
package copyroles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	rolesql "github.com/onebox-faas/faas/pkg/managedpostgres/copyroles/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type membershipFixture struct {
	*fixture
	seed Receipt
	plan MembershipPlan
}

func newMembershipFixture(t *testing.T, super bool, configure ...func(*fixture)) membershipFixture {
	t.Helper()
	f := newFixture(t)
	if super {
		for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
			if _, err := root.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{f.owner}.Sanitize()+" SUPERUSER"); err != nil {
				t.Fatal(err)
			}
		}
		f.targetInventory, _ = readCatalogue(t, f.target)
	}
	// The ordinary creator's required target maintenance grants have exactly the
	// same grantors/options as the source graph, so no extra authority is hidden.
	for _, role := range []string{f.member, f.group} {
		if _, err := f.sourceRoot.Exec(t.Context(), "GRANT "+pgx.Identifier{role}.Sanitize()+" TO "+pgx.Identifier{f.owner}.Sanitize()+" WITH ADMIN TRUE, INHERIT FALSE, SET FALSE"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.source.Exec(t.Context(), "GRANT "+pgx.Identifier{f.group}.Sanitize()+" TO "+pgx.Identifier{f.member}.Sanitize()+" WITH ADMIN FALSE, INHERIT FALSE, SET TRUE GRANTED BY "+pgx.Identifier{f.owner}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	for _, prepare := range configure {
		prepare(f)
	}
	f.refreshSource(t)
	src, _ := f.sourcePlan.RoleCatalogueForWorker()
	base, _ := f.targetInventory.RoleCatalogueForWorker()
	ids := map[string]uint32{}
	for _, role := range base.Roles {
		ids[role.Name] = role.OID
	}
	f.dispositions = nil
	for _, role := range src.Roles {
		f.dispositions = append(f.dispositions, Disposition{SourceOID: role.OID, ExistingTargetOID: ids[role.Name]})
	}
	seed, err := Prepare(t.Context(), f.target, f.plan(t), allow)
	if err != nil {
		t.Fatal(err)
	}
	i, _ := readCatalogue(t, f.target)
	plan, err := NewMembershipPlan(f.sourcePlan, seed, i)
	if err != nil {
		t.Fatal(err)
	}
	return membershipFixture{f, seed, plan}
}

func assertMembershipGraph(t *testing.T, f membershipFixture) {
	t.Helper()
	i, _ := readCatalogue(t, f.target)
	c, err := i.MembershipCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	sortMemberships(c.Memberships)
	if !reflect.DeepEqual(c.Memberships, f.plan.body.Desired) {
		t.Fatal("materialized graph differs in role/member/grantor identity or options")
	}
	var private bool
	if err = f.targetRoot.QueryRow(t.Context(), "SELECT NOT rolcanlogin AND rolpassword IS NULL FROM pg_authid WHERE rolname=$1", f.member).Scan(&private); err != nil || !private {
		t.Fatalf("membership materialization enabled credentials: %v", err)
	}
}

func TestMembershipsOrdinaryOwnerPreservesExactGrantorOptionsAndOriginalSource(t *testing.T) {
	f := newMembershipFixture(t, false)
	key, _ := age.GenerateX25519Identity()
	sealed, err := SealMemberships(key.Recipient(), f.plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{f.plan, sealed} {
		out, _ := json.Marshal(value)
		for _, secret := range []string{f.owner, f.member, f.group, "private-value", "source-password-never-copied"} {
			if bytes.Contains(out, []byte(secret)) || bytes.Contains(sealed.Ciphertext, []byte(secret)) || strings.Contains(fmt.Sprintf("%+v %#v", value, value), secret) {
				t.Fatal("membership plan leaked private metadata")
			}
		}
	}
	c, err := f.sourcePlan.MembershipCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	c.Memberships[0].Grantor = "caller-changed"
	c.Roles.Roles[0].Name = "caller-changed"
	if _, err = OpenMemberships([]*age.X25519Identity{key}, f.sourcePlan, f.seed, sealed); err != nil {
		t.Fatalf("worker catalogue aliases original inventory: %v", err)
	}
	r, err := ApplyMemberships(t.Context(), f.target, f.plan, allow)
	if err != nil || r.AppliedAt().IsZero() {
		t.Fatalf("ordinary membership materialization: %v", err)
	}
	assertMembershipGraph(t, f)
	i, _ := readCatalogue(t, f.source)
	source, _ := i.MembershipCatalogueForWorker()
	sortMemberships(source.Memberships)
	if !reflect.DeepEqual(source.Memberships, f.plan.body.SourceMemberships) {
		t.Fatal("target membership writes changed the source graph")
	}
	retry, err := ApplyMemberships(t.Context(), f.target, f.plan, allow)
	if err != nil || !retry.AppliedAt().Equal(r.AppliedAt()) {
		t.Fatalf("exact membership retry replaced ownership time: %v", err)
	}
}

func TestMembershipsPreserveMultipleGrantorsDependenciesAndRemoveConflictingTargetGrants(t *testing.T) {
	f := newMembershipFixture(t, true, func(f *fixture) {
		delegate := "delegate/ ' ; --" + f.owner[len(f.owner)-12:]
		if _, err := f.sourceRoot.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{delegate}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
				_, _ = root.Exec(context.Background(), "REVOKE "+pgx.Identifier{f.group}.Sanitize()+" FROM "+pgx.Identifier{delegate}.Sanitize()+" GRANTED BY "+pgx.Identifier{f.owner}.Sanitize()+" CASCADE")
				if _, err := root.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{delegate}.Sanitize()); err != nil {
					t.Error(err)
				}
			}
		})
		for _, query := range []string{
			"GRANT " + pgx.Identifier{f.group}.Sanitize() + " TO " + pgx.Identifier{delegate}.Sanitize() + " WITH ADMIN TRUE, INHERIT TRUE, SET FALSE GRANTED BY " + pgx.Identifier{f.owner}.Sanitize(),
			"GRANT " + pgx.Identifier{f.group}.Sanitize() + " TO " + pgx.Identifier{f.member}.Sanitize() + " WITH ADMIN FALSE, INHERIT TRUE, SET FALSE GRANTED BY " + pgx.Identifier{delegate}.Sanitize(),
		} {
			if _, err := f.source.Exec(t.Context(), query); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.sourceRoot.Exec(t.Context(), "GRANT "+pgx.Identifier{f.group}.Sanitize()+" TO "+pgx.Identifier{f.member}.Sanitize()+" WITH ADMIN TRUE, INHERIT FALSE, SET TRUE"); err != nil {
			t.Fatal(err)
		}
	})
	// A conflicting reverse edge is explicit pre-write target input. The first
	// grant pass cannot form a cycle; RESTRICT removes it before the next pass.
	if _, err := f.targetRoot.Exec(t.Context(), "GRANT "+pgx.Identifier{f.member}.Sanitize()+" TO "+pgx.Identifier{f.group}.Sanitize()+" WITH ADMIN TRUE, INHERIT TRUE, SET TRUE"); err != nil {
		t.Fatal(err)
	}
	i, _ := readCatalogue(t, f.target)
	var err error
	f.plan, err = NewMembershipPlan(f.sourcePlan, f.seed, i)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ApplyMemberships(t.Context(), f.target, f.plan, allow); err != nil || got.AppliedAt().IsZero() {
		t.Fatalf("dependent multigrantor graph: %v", err)
	}
	assertMembershipGraph(t, f)
}

func TestMembershipsSealingRejectsRotationRelabelSeedScopeAndCatalogueSubstitution(t *testing.T) {
	f := newMembershipFixture(t, false)
	key, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	s, err := SealMemberships(key.Recipient(), f.plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenMemberships([]*age.X25519Identity{other, nil, key}, f.sourcePlan, f.seed, s); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverMemberships([]*age.X25519Identity{other, nil, key}, f.sourcePlan, f.seed.plan, s)
	if err != nil || !reflect.DeepEqual(recovered.body, f.plan.body) {
		t.Fatalf("SQL-free recovery lost original seed identities/time or graph: %v", err)
	}
	changedRolePlan := f.seed.plan
	changedRolePlan.body.TargetFingerprint = strings.Repeat("f", 64)
	if _, err = RecoverMemberships([]*age.X25519Identity{key}, f.sourcePlan, changedRolePlan, s); err == nil {
		t.Fatal("SQL-free recovery accepted a different original role plan")
	}
	for _, fault := range []string{"recipient", "scope", "inventory", "target", "seed", "namespace", "source"} {
		t.Run(fault, func(t *testing.T) {
			changed, seed, source := s, f.seed, f.sourcePlan
			switch fault {
			case "recipient":
				changed.KeyID = other.Recipient().String()
			case "scope":
				changed.Scope.SourceVersion = strings.Repeat("f", 64)
			case "inventory":
				changed.InventoryFingerprint = strings.Repeat("f", 64)
			case "target":
				changed.TargetFingerprint = strings.Repeat("f", 64)
			case "seed":
				seed.body.SeededAt = seed.body.SeededAt.AddDate(0, 0, 1)
			case "namespace":
				role, err := Seal(key.Recipient(), f.seed.plan)
				if err != nil {
					t.Fatal(err)
				}
				changed = SealedMemberships(role)
			case "source":
				source = copyinventory.ExportPlan{}
			}
			if _, err := OpenMemberships([]*age.X25519Identity{key, other}, source, seed, changed); err == nil {
				t.Fatal("substituted membership plan recovered")
			}
		})
	}
}

func TestMembershipsLostCommittedReplyRecoversOriginalGraphAndRejectsDrift(t *testing.T) {
	f := newMembershipFixture(t, false)
	var checks atomic.Int32
	authorize := func(context.Context, copyarchive.RestoreTarget) error {
		if checks.Add(1) == 4 {
			return errors.New("committed reply lost")
		}
		return nil
	}
	if got, err := ApplyMemberships(t.Context(), f.target, f.plan, authorize); !errors.Is(err, pgerrors.ErrUnavailable) || !got.AppliedAt().IsZero() {
		t.Fatalf("lost committed reply became completion: %v", err)
	}
	assertMembershipGraph(t, f)
	r, err := ApplyMemberships(t.Context(), f.target, f.plan, allow)
	if err != nil || r.AppliedAt().IsZero() {
		t.Fatalf("membership journal recovery: %v", err)
	}
	if _, err = f.target.Exec(t.Context(), "GRANT "+pgx.Identifier{f.group}.Sanitize()+" TO "+pgx.Identifier{f.member}.Sanitize()+" WITH INHERIT TRUE GRANTED BY "+pgx.Identifier{f.owner}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if got, err := ApplyMemberships(t.Context(), f.target, f.plan, allow); !errors.Is(err, pgerrors.ErrConflict) || !got.AppliedAt().IsZero() {
		t.Fatalf("journal replay ignored option drift: %v", err)
	}
}

func TestMembershipsSQLFreeRecoveryStillRequiresOriginalTargetSeedJournalBeforeGrants(t *testing.T) {
	f := newMembershipFixture(t, false)
	raw, err := f.plan.PrivatePayloadForSealing()
	if err != nil {
		t.Fatal(err)
	}
	var changed MembershipPlan
	if err = json.Unmarshal(raw, &changed.body); err != nil {
		t.Fatal(err)
	}
	changed.body.SeedReceipt.SeededAt = changed.body.SeedReceipt.SeededAt.AddDate(0, 0, 1)
	key, _ := age.GenerateX25519Identity()
	sealed, err := SealMemberships(key.Recipient(), changed)
	if err != nil {
		t.Fatal(err)
	}
	// Metadata recovery does not claim that the embedded seed was committed.
	// The authenticated target's original transaction journal decides that.
	plan, err := RecoverMemberships([]*age.X25519Identity{key}, f.sourcePlan, f.seed.plan, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := ApplyMemberships(t.Context(), f.target, plan, allow); !errors.Is(err, pgerrors.ErrConflict) || !r.AppliedAt().IsZero() {
		t.Fatalf("recovered metadata replaced the original target seed journal: %v", err)
	}
	i, _ := readCatalogue(t, f.target)
	c, _ := i.MembershipCatalogueForWorker()
	sortMemberships(c.Memberships)
	if !reflect.DeepEqual(c.Memberships, f.plan.body.Baseline) {
		t.Fatal("rejected seed journal changed the target grant graph")
	}
}

func TestMembershipsRejectedPrecommitAndMissingAuthorityRollBackGraphAndReceipt(t *testing.T) {
	for _, fault := range []string{"precommit", "authority", "shared_journal"} {
		t.Run(fault, func(t *testing.T) {
			var configure []func(*fixture)
			if fault == "authority" {
				configure = append(configure, func(f *fixture) {
					if _, err := f.sourceRoot.Exec(t.Context(), "GRANT "+pgx.Identifier{f.group}.Sanitize()+" TO "+pgx.Identifier{f.member}.Sanitize()+" WITH ADMIN FALSE, INHERIT TRUE, SET FALSE"); err != nil {
						t.Fatal(err)
					}
				})
			}
			f := newMembershipFixture(t, false, configure...)
			baseline, _ := readCatalogue(t, f.target)
			before, _ := baseline.MembershipCatalogueForWorker()
			authorize := allow
			want := pgerrors.ErrConflict
			switch fault {
			case "precommit":
				var n atomic.Int32
				authorize = func(context.Context, copyarchive.RestoreTarget) error {
					if n.Add(1) == 3 {
						return pgerrors.ErrConflict
					}
					return nil
				}
			case "authority":
				// The original desired graph requires a bootstrap grantor that
				// this ordinary target actor cannot impersonate. No fallback grantor.
				want = pgerrors.ErrUnsupported
			case "shared_journal":
				if _, err := f.target.Exec(t.Context(), "CREATE SCHEMA gregale_copy_memberships; CREATE TABLE gregale_copy_memberships.receipt(singleton boolean PRIMARY KEY,version int,plan jsonb,applied_at timestamptz); GRANT USAGE ON SCHEMA gregale_copy_memberships TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := ApplyMemberships(t.Context(), f.target, f.plan, authorize); !errors.Is(err, want) || !got.AppliedAt().IsZero() {
				t.Fatalf("rejected membership materialization: %v", err)
			}
			after, _ := readCatalogue(t, f.target)
			actual, _ := after.MembershipCatalogueForWorker()
			sortMemberships(before.Memberships)
			sortMemberships(actual.Memberships)
			if !reflect.DeepEqual(actual.Memberships, before.Memberships) {
				t.Fatal("failed transaction changed the target graph")
			}
		})
	}
}

func TestMembershipsConcurrentCommitAndStaleLockWaiterRetainOriginalAuthority(t *testing.T) {
	t.Run("concurrent", func(t *testing.T) {
		f := newMembershipFixture(t, false)
		other, err := pgx.ConnectConfig(t.Context(), f.target.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close(context.Background())
		type result struct {
			receipt MembershipReceipt
			err     error
		}
		done := make(chan result, 2)
		for _, c := range []*pgx.Conn{f.target, other} {
			go func(c *pgx.Conn) { r, err := ApplyMemberships(t.Context(), c, f.plan, allow); done <- result{r, err} }(c)
		}
		a, b := <-done, <-done
		if a.err != nil || b.err != nil || !a.receipt.AppliedAt().Equal(b.receipt.AppliedAt()) {
			t.Fatalf("concurrent membership commit: %v / %v", a.err, b.err)
		}
		assertMembershipGraph(t, f)
	})
	t.Run("stale_waiter", func(t *testing.T) {
		f := newMembershipFixture(t, false)
		blocker, err := pgx.ConnectConfig(t.Context(), f.target.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Close(context.Background())
		tx, err := blocker.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err = rolesql.New().LockRoleSeed(t.Context(), tx); err != nil {
			t.Fatal(err)
		}
		var owner atomic.Bool
		owner.Store(true)
		checked, done := make(chan struct{}, 1), make(chan error, 1)
		go func() {
			_, err := ApplyMemberships(t.Context(), f.target, f.plan, func(context.Context, copyarchive.RestoreTarget) error {
				if !owner.Load() {
					return pgerrors.ErrConflict
				}
				select {
				case checked <- struct{}{}:
				default:
				}
				return nil
			})
			done <- err
		}()
		<-checked
		owner.Store(false)
		if err = tx.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = <-done; !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatalf("stale waiting worker kept mutation authority: %v", err)
		}
		var absent bool
		if err = f.target.QueryRow(t.Context(), "SELECT to_regnamespace('gregale_copy_memberships') IS NULL").Scan(&absent); err != nil || !absent {
			t.Fatalf("stale worker installed ownership: %v", err)
		}
	})
}

func TestMembershipsOriginalMetadataAndTargetBaselineCannotBeRebased(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		f := newMembershipFixture(t, false)
		original := f.sourcePlan
		key, _ := age.GenerateX25519Identity()
		sealed, err := SealMemberships(key.Recipient(), f.plan)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.source.Exec(t.Context(), "GRANT "+pgx.Identifier{f.group}.Sanitize()+" TO "+pgx.Identifier{f.member}.Sanitize()+" WITH INHERIT TRUE GRANTED BY "+pgx.Identifier{f.owner}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		f.refreshSource(t)
		i, _ := readCatalogue(t, f.target)
		if _, err = NewMembershipPlan(f.sourcePlan, f.seed, i); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatalf("new source graph rebased original seed: %v", err)
		}
		if _, err = OpenMemberships([]*age.X25519Identity{key}, f.sourcePlan, f.seed, sealed); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatalf("new source graph reopened original membership plan: %v", err)
		}
		p, err := OpenMemberships([]*age.X25519Identity{key}, original, f.seed, sealed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ApplyMemberships(t.Context(), f.target, p, allow); err != nil {
			t.Fatalf("original metadata recovery required a source reread: %v", err)
		}
		assertMembershipGraph(t, f)
	})
	t.Run("target", func(t *testing.T) {
		f := newMembershipFixture(t, false)
		if _, err := f.targetRoot.Exec(t.Context(), "GRANT "+pgx.Identifier{f.group}.Sanitize()+" TO "+pgx.Identifier{f.owner}.Sanitize()+" WITH SET TRUE"); err != nil {
			t.Fatal(err)
		}
		if got, err := ApplyMemberships(t.Context(), f.target, f.plan, allow); !errors.Is(err, pgerrors.ErrConflict) || !got.AppliedAt().IsZero() {
			t.Fatalf("target baseline drift was adopted: %v", err)
		}
		var absent bool
		if err := f.target.QueryRow(t.Context(), "SELECT to_regnamespace('gregale_copy_memberships') IS NULL").Scan(&absent); err != nil || !absent {
			t.Fatalf("baseline conflict retained an owner: %v", err)
		}
	})
}

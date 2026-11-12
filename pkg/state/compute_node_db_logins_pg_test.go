//go:build !no_pg

// adr: 20261110210000
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 1c-1: a login mapped to a node writes only that node's rows, cannot
// change its identity, labels or addresses, and cannot raise its own
// capacity; an unmapped login behaves as before.
func TestComputeNodeDBLoginsScopeWrites(t *testing.T) {
	store, owner := lifecycleCompletionPGStore(t)
	ctx := t.Context()
	nodeLogin, login := ownerAuthorityLogin(t, owner, false)
	unmapped, _ := ownerAuthorityLogin(t, owner, false)
	a := completionNode(t, store, state.NodeLifecycleActive)
	b := completionNode(t, store, state.NodeLifecycleActive)
	if _, err := owner.Exec(ctx, `update compute_nodes set region = 'eu-1' where id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `insert into compute_node_db_logins (login, node_name) values ($1, $2)`, login, a.Name); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) error {
		_, err := nodeLogin.Exec(ctx, sql, args...)
		return err
	}
	capacity := func(id string) (ceiling, mem int) {
		t.Helper()
		if err := owner.QueryRow(ctx, `select admission_ceiling_mb, mem_mb from compute_nodes where id = $1`, id).Scan(&ceiling, &mem); err != nil {
			t.Fatal(err)
		}
		return ceiling, mem
	}

	if err := exec(`update compute_nodes set last_heartbeat_at = now(), readiness = 'degraded' where id = $1`, a.ID); err != nil {
		t.Fatalf("own row heartbeat refused: %v", err)
	}
	for name, err := range map[string]error{
		"another node's row":      exec(`update compute_nodes set last_heartbeat_at = now() where id = $1`, b.ID),
		"another node's ceiling":  exec(`update compute_nodes set admission_ceiling_mb = 1 where id = $1`, b.ID),
		"deleting its own row":    exec(`delete from compute_nodes where id = $1`, a.ID),
		"renaming itself":         exec(`update compute_nodes set name = $2 where id = $1`, a.ID, b.Name+"-x"),
		"changing its id":         exec(`update compute_nodes set id = gen_random_uuid() where id = $1`, a.ID),
		"moving region":           exec(`update compute_nodes set region = 'us-1' where id = $1`, a.ID),
		"redirecting its address": exec(`update compute_nodes set target_url = 'tcp://10.0.0.9:7000' where id = $1`, a.ID),
		"registering another":     exec(`insert into compute_nodes (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb) values ('rogue', 'tcp://10.0.0.9:7000', 1, 1, 1, 1)`),
		"another node's memory":   exec(`insert into node_memory_reports (node_id, measured_mb, frozen_instances, measured_at) values ($1, 1, 0, now())`, b.ID),
		"another node's beat":     exec(`insert into compute_node_heartbeats (node_id, last_heartbeat_at, source) values ($1, now(), 'heartbeat_tick')`, b.ID),
	} {
		if sqlState(err) != "42501" {
			t.Errorf("%s = %v, want SQLSTATE 42501", name, err)
		}
	}
	if err := exec(`insert into node_memory_reports (node_id, measured_mb, frozen_instances, measured_at) values ($1, 1, 0, now())`, a.ID); err != nil {
		t.Errorf("own memory report refused: %v", err)
	}
	if err := exec(`insert into compute_node_heartbeats (node_id, last_heartbeat_at, source) values ($1, now(), 'heartbeat_tick')`, a.ID); err != nil {
		t.Errorf("own heartbeat row refused: %v", err)
	}

	// A raise is clamped; a cut applies.
	if err := exec(`update compute_nodes set admission_ceiling_mb = 999999, mem_mb = 999999 where id = $1`, a.ID); err != nil {
		t.Fatalf("capacity raise refused instead of clamped: %v", err)
	}
	if ceiling, mem := capacity(a.ID); ceiling != 8192 || mem != 8192 {
		t.Fatalf("raise not clamped: ceiling=%d mem=%d", ceiling, mem)
	}
	if err := exec(`update compute_nodes set admission_ceiling_mb = 4096 where id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if ceiling, _ := capacity(a.ID); ceiling != 4096 {
		t.Fatalf("cut not applied: ceiling=%d", ceiling)
	}

	// vmmd's self-registration upserts by name: its own name works (and
	// cannot raise capacity); another node's name is refused.
	reg := state.NewPgStore(nodeLogin)
	self := state.ComputeNode{Name: a.Name, TargetURL: a.TargetURL, VPCPUs: 64, VCPUBudget: 160, MemMB: 65536,
		AdmissionCeilingMB: 65536, MaxConcurrency: 16}
	if _, err := reg.UpsertComputeNodeFromVmmd(ctx, self); err != nil {
		t.Fatalf("own self-registration refused: %v", err)
	}
	if ceiling, mem := capacity(a.ID); ceiling != 4096 || mem != 8192 {
		t.Fatalf("self-registration raised capacity: ceiling=%d mem=%d", ceiling, mem)
	}
	other := self
	other.Name, other.TargetURL = b.Name, b.TargetURL
	if _, err := reg.UpsertComputeNodeFromVmmd(ctx, other); sqlState(err) != "42501" {
		t.Fatalf("registering as another node = %v, want SQLSTATE 42501", err)
	}

	// Slice 1c-2: the control plane enrolls a node at join, before vmmd has
	// registered it. Until then the login can report for no node; its first
	// registration is admitted under its own name only.
	freshLogin, fresh := ownerAuthorityLogin(t, owner, false)
	freshName := a.Name + "-fresh"
	if _, err := owner.Exec(ctx, `insert into compute_node_db_logins (login, node_name) values ($1, $2)`, fresh, freshName); err != nil {
		t.Fatal(err)
	}
	if _, err := freshLogin.Exec(ctx, `insert into node_memory_reports (node_id, measured_mb, frozen_instances, measured_at) values ($1, 1, 0, now())`, b.ID); sqlState(err) != "42501" {
		t.Fatalf("an unregistered node reporting for another = %v, want SQLSTATE 42501", err)
	}
	freshReg := state.NewPgStore(freshLogin)
	if _, err := freshReg.UpsertComputeNodeFromVmmd(ctx, other); sqlState(err) != "42501" {
		t.Fatalf("an unregistered node registering as another = %v, want SQLSTATE 42501", err)
	}
	first := self
	first.Name, first.TargetURL = freshName, "tcp://10.0.0.77:7000"
	registered, err := freshReg.UpsertComputeNodeFromVmmd(ctx, first)
	if err != nil {
		t.Fatalf("first registration under its own name refused: %v", err)
	}
	if _, err := freshLogin.Exec(ctx, `insert into node_memory_reports (node_id, measured_mb, frozen_instances, measured_at) values ($1, 1, 0, now())`, registered.ID); err != nil {
		t.Fatalf("registered node's own report refused: %v", err)
	}

	// An unmapped login is unchanged, and the control plane raises capacity.
	if _, err := unmapped.Exec(ctx, `update compute_nodes set last_heartbeat_at = $2 where id = $1`, b.ID, time.Now()); err != nil {
		t.Fatalf("unmapped login refused: %v", err)
	}
	if _, err := owner.Exec(ctx, `update compute_nodes set admission_ceiling_mb = 16384 where id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if ceiling, _ := capacity(a.ID); ceiling != 16384 {
		t.Fatalf("owner raise = %d", ceiling)
	}
}

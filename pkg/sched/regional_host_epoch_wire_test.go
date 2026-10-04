package sched_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

type journalEpochVMM struct {
	*fakeVMM
	journal *runtimequotahost.Journal
}

func (v *journalEpochVMM) RegionalRuntimeHostEpoch() (runtimequota.HostEpoch, error) {
	return v.journal.RuntimeHostEpoch()
}

func TestVMMClientRegionalHostEpochRestartAndReboot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	node, boot := uuid.NewString(), uuid.NewString()
	j, err := runtimequotahost.Open(dir, node, boot)
	if err != nil {
		t.Fatal(err)
	}
	v := &journalEpochVMM{fakeVMM: &fakeVMM{}, journal: j}
	t.Cleanup(func() { _ = v.journal.Close() })
	c := newClient(t, v)
	stats, err := c.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first, err := stats.RegionalRuntimeEpochForNode(node)
	if err != nil || first.HostEpochID != j.Identity().EpochID {
		t.Fatalf("wire lost journal identity: %+v %v", first, err)
	}
	if _, err := stats.RegionalRuntimeEpochForNode(uuid.NewString()); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatalf("cross-node sample accepted: %v", err)
	}
	for _, nextBoot := range []string{boot, uuid.NewString()} {
		if err := v.journal.Close(); err != nil {
			t.Fatal(err)
		}
		closed, err := c.Stats(t.Context())
		if err != nil || closed.RegionalRuntimeHostEpoch != nil {
			t.Fatalf("closed journal discovery affected ordinary stats: %+v %v", closed, err)
		}
		if _, err := closed.RegionalRuntimeEpochForNode(node); !errors.Is(err, runtimequota.ErrTrust) {
			t.Fatalf("closed owner accepted as regional destination: %v", err)
		}
		next, err := runtimequotahost.Open(dir, node, nextBoot)
		if err != nil {
			t.Fatal(err)
		}
		v.journal = next
		current, err := c.Stats(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		epoch, err := current.RegionalRuntimeEpochForNode(node)
		if err != nil || (epoch == first) != (nextBoot == boot) {
			t.Fatalf("restart/reboot changed wrong incarnation: %+v %v", epoch, err)
		}
	}
	if *stats.RegionalRuntimeHostEpoch != first {
		t.Fatal("later discovery mutated the original sample")
	}
	legacy, err := newClient(t, &fakeVMM{}).Stats(t.Context())
	if err != nil || legacy.RegionalRuntimeHostEpoch != nil {
		t.Fatalf("legacy host stats: %+v %v", legacy, err)
	}
	if _, err := legacy.RegionalRuntimeEpochForNode(node); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatalf("legacy host accepted as regional destination: %v", err)
	}
}

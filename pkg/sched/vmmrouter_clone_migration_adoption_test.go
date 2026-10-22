// adr: 590
package sched

import (
	"context"
	"errors"
	"testing"
)

type cloneAdoptionVMM struct {
	fakeRouterVMM
	calls int
	reply error
}

func (f *cloneAdoptionVMM) AdoptMigratedInstance(context.Context, string, string, AppSpec, string, string, string) (LiveMigrationAdopt, error) {
	f.calls++
	return LiveMigrationAdopt{}, f.reply
}

func TestVMMRouterCloneAdoptionRecordsBeforeRPC(t *testing.T) {
	denied := errors.New("fenced")
	lost := errors.New("lost reply")
	cli := &cloneAdoptionVMM{reply: lost}
	r := NewVMMRouter(nil, nil, nil)
	r.SetClient("destination", cli)
	allow := false
	recorded := false
	r.WithCloneMigrationAdoptionRecorder(func(_ context.Context, instance, app, dep, dest, mem, vmstate, token string) error {
		if instance != "instance" || app != "app" || dep != "deployment" || dest != "destination" || mem != "mem" || vmstate != "state" || token != "original-token" {
			t.Fatal("adoption identity changed")
		}
		if !allow || recorded {
			return denied
		}
		if cli.calls != 0 {
			t.Fatal("RPC before durable record")
		}
		recorded = true
		return nil
	})
	call := func() error {
		_, err := r.AdoptMigratedInstance(t.Context(), "destination", "instance", AppSpec{AppID: "app", DeploymentID: "deployment"}, "mem", "state", "original-token")
		return err
	}
	if err := call(); !errors.Is(err, denied) || cli.calls != 0 {
		t.Fatal("fenced RPC sent", err)
	}
	allow = true
	if err := call(); !errors.Is(err, lost) || cli.calls != 1 || !recorded {
		t.Fatal("original dispatch lost", err)
	}
	if err := call(); !errors.Is(err, denied) || cli.calls != 1 {
		t.Fatal("unknown outcome redispatched", err)
	}
}

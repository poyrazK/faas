// adr: 590
package sched

import (
	"context"
	"errors"
	"testing"
)

type cloneResumeVMM struct {
	fakeRouterVMM
	calls int
	reply error
}

func (f *cloneResumeVMM) CancelLiveMigration(context.Context, string, string, string) error {
	f.calls++
	return f.reply
}

func TestVMMRouterCloneResumeRecordsBeforeRPC(t *testing.T) {
	denied := errors.New("fenced")
	lost := errors.New("lost reply")
	cli := &cloneResumeVMM{reply: lost}
	r := NewVMMRouter(nil, nil, nil)
	r.SetClient("source", cli)
	allow := false
	recorded := false
	r.WithCloneMigrationResumeRecorder(func(_ context.Context, instance, source, token string) error {
		if instance != "instance" || source != "source" || token != "original-token" {
			t.Fatal("resume identity changed")
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
		return r.CancelLiveMigration(t.Context(), "source", "instance", "original-token")
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

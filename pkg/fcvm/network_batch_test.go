package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

type networkBatchCall struct {
	argv  []string
	input string
}

type networkBatchRunner struct {
	calls []networkBatchCall
	err   error
}

func (r *networkBatchRunner) Run(_ context.Context, argv []string) error {
	r.calls = append(r.calls, networkBatchCall{argv: argv})
	return nil
}

func (r *networkBatchRunner) RunInput(_ context.Context, argv []string, input []byte) error {
	r.calls = append(r.calls, networkBatchCall{argv: argv, input: string(input)})
	return r.err
}

func TestIPSetupBatchPreservesNamespaceAndOrder(t *testing.T) {
	run := &networkBatchRunner{}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("batch", "fc-batch", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	nc.TapUID = 20001
	want := nc.SetupCommands()
	if err := m.runIPSetupCommands(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if len(run.calls) != 6 {
		t.Fatalf("setup processes = %d, want 6 instead of 13", len(run.calls))
	}
	var got [][]string
	for _, call := range run.calls {
		if call.input == "" {
			got = append(got, call.argv)
			continue
		}
		prefix := call.argv[:len(call.argv)-2]
		for _, line := range strings.Split(strings.TrimSuffix(call.input, "\n"), "\n") {
			got = append(got, append(append([]string{}, prefix...), strings.Fields(line)...))
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batched setup changed topology or ordering:\ngot %v\nwant %v", got, want)
	}
}

func TestIPSetupBatchFailureRollsBackWake(t *testing.T) {
	wantErr := errors.New("batch failed")
	run := &networkBatchRunner{err: wantErr}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)
	_, err := m.ColdBoot(context.Background(), req("batch-fail"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("wake error = %v, want batch failure", err)
	}
	if vmm.boots() != 0 || m.LeasedCount() != 0 || m.LiveCount() != 0 {
		t.Fatalf("failed network booted or leaked: boots=%d leases=%d live=%d", vmm.boots(), m.LeasedCount(), m.LiveCount())
	}
	for _, call := range run.calls {
		if strings.Contains(strings.Join(call.argv, " "), "sysctl") {
			t.Fatal("setup continued after the first failed batch")
		}
	}
}

func TestIPSetupBatchKeepsUnusualArgumentsLiteral(t *testing.T) {
	for _, name := range []string{"vh\nlink del br-tenants", "vh name", "vh#comment", "vh\\name", "vh\"name", "vh'name", "vh\x00name"} {
		t.Run(name, func(t *testing.T) {
			run := &networkBatchRunner{}
			m := newTestManager(run, &fakeVMM{})
			cmds := [][]string{{"ip", "link", "set", name, "up"}, {"ip", "link", "set", "vh2", "up"}}
			if err := m.runIPSetupCommands(context.Background(), cmds); err != nil {
				t.Fatal(err)
			}
			if len(run.calls) != 2 || run.calls[0].input != "" || !reflect.DeepEqual(run.calls[0].argv, cmds[0]) {
				t.Fatalf("unusual argument was serialized into a batch: %#v", run.calls)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

type fakeAdoptionAdmin struct {
	vmmdpb.UnimplementedVmmdServer
	records []*vmmdpb.AdoptionInventoryRecord
	report  *vmmdpb.AdoptionReportResponse
	err     error
}

func (f *fakeAdoptionAdmin) AdoptionInventory(context.Context, *vmmdpb.AdoptionInventoryRequest) (*vmmdpb.AdoptionInventoryResponse, error) {
	return &vmmdpb.AdoptionInventoryResponse{Records: f.records}, f.err
}

func (f *fakeAdoptionAdmin) AdoptionReport(context.Context, *vmmdpb.AdoptionReportRequest) (*vmmdpb.AdoptionReportResponse, error) {
	return f.report, f.err
}

func serveFakeAdoptionAdmin(t *testing.T, f *fakeAdoptionAdmin) string {
	t.Helper()
	// Short path: unix socket paths are limited to ~104 bytes on darwin.
	dir, err := os.MkdirTemp("", "vmmd-admin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "admin.sock")
	lis, err := vmmdgrpc.ListenAdoptionAdmin(socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	if info, err := os.Stat(socket); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("admin socket mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	return socket
}

func writeAdoptionConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAdoptionCheckCommand(t *testing.T) {
	adopting := "adoption_record = true\nadopt_running_vms = true\n"
	for _, c := range []struct {
		name    string
		config  string
		admin   *fakeAdoptionAdmin
		noSock  bool
		want    int
		reason  bool
		unadopt int
	}{
		{name: "empty node", config: adopting, admin: &fakeAdoptionAdmin{}, want: adoptionExitOK},
		{name: "undecodable record", config: adopting, admin: &fakeAdoptionAdmin{records: []*vmmdpb.AdoptionInventoryRecord{{Record: []byte(`{"version":99}`), ProofOk: true}}}, want: adoptionExitNotYet, unadopt: 1},
		{name: "candidate does not adopt", config: "adoption_record = true\n", admin: &fakeAdoptionAdmin{}, want: adoptionExitNotYet, reason: true},
		{name: "running vmmd has no inventory", config: adopting, admin: &fakeAdoptionAdmin{err: status.Error(codes.Unimplemented, "old vmmd")}, want: adoptionExitNotYet, reason: true},
		{name: "no running vmmd", config: adopting, noSock: true, want: adoptionExitNotYet, reason: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "absent.sock")
			if !c.noSock {
				socket = serveFakeAdoptionAdmin(t, c.admin)
			}
			var out, errOut bytes.Buffer
			code := adoptionCheckCommand([]string{"--config", writeAdoptionConfig(t, c.config), "--socket", socket, "--timeout", "2s"}, &out, &errOut)
			var res adoptionCheckResult
			if err := json.Unmarshal(out.Bytes(), &res); err != nil {
				t.Fatalf("output %q: %v (stderr %s)", out.String(), err, errOut.String())
			}
			if code != c.want || (res.Reason != "") != c.reason || len(res.Unadoptable) != c.unadopt || res.AllAdoptable != (c.want == adoptionExitOK) {
				t.Fatalf("code %d result %+v; want code %d reason=%v unadoptable=%d", code, res, c.want, c.reason, c.unadopt)
			}
		})
	}
}

func TestAdoptionReportCommand(t *testing.T) {
	clean := &vmmdpb.AdoptionReportResponse{NodeId: "n", Epoch: 4, Incarnation: "i", Complete: true,
		Adopted: []*vmmdpb.AdoptionReportInstance{{Instance: "a"}},
		Refused: []*vmmdpb.AdoptionReportInstance{{Instance: "s", Resolved: "stopped"}, {Instance: "k", Resolved: "killed"}}}
	refused := &vmmdpb.AdoptionReportResponse{NodeId: "n", Epoch: 4, Incarnation: "i", Complete: true,
		Refused: []*vmmdpb.AdoptionReportInstance{{Instance: "r", Reason: "process_absent"}}}
	fenced := &vmmdpb.AdoptionReportResponse{NodeId: "n", Epoch: 4, Incarnation: "i", Complete: true, Fenced: true}
	for _, c := range []struct {
		name  string
		admin *fakeAdoptionAdmin
		want  int
	}{
		{name: "clean", admin: &fakeAdoptionAdmin{report: clean}, want: adoptionExitOK},
		{name: "refusals", admin: &fakeAdoptionAdmin{report: refused}, want: adoptionExitNotYet},
		{name: "fenced", admin: &fakeAdoptionAdmin{report: fenced}, want: adoptionExitNotYet},
		{name: "adoption off", admin: &fakeAdoptionAdmin{err: status.Error(codes.Unimplemented, "off")}, want: adoptionExitNotYet},
		{name: "never ready", admin: &fakeAdoptionAdmin{err: status.Error(codes.Unavailable, "no epoch")}, want: adoptionExitError},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := adoptionReportCommand([]string{"--socket", serveFakeAdoptionAdmin(t, c.admin), "--wait", "0s"}, &out, &errOut)
			var res adoptionReportResult
			if err := json.Unmarshal(out.Bytes(), &res); err != nil {
				t.Fatalf("output %q: %v", out.String(), err)
			}
			if code != c.want || res.Clean != (c.want == adoptionExitOK) {
				t.Fatalf("code %d result %+v; want %d", code, res, c.want)
			}
			if c.name == "clean" && (res.Adopted != 1 || res.Stopped != 1 || res.Killed != 1 || len(res.Refused) != 0) {
				t.Fatalf("clean report summary = %+v", res)
			}
		})
	}
}

func TestRunAdoptionCommandLeavesTheDaemonAlone(t *testing.T) {
	if _, ok := runAdoptionCommand(nil, &bytes.Buffer{}, &bytes.Buffer{}); ok {
		t.Fatal("no arguments must start the daemon")
	}
	if _, ok := runAdoptionCommand([]string{"--help"}, &bytes.Buffer{}, &bytes.Buffer{}); ok {
		t.Fatal("other arguments must start the daemon")
	}
}

func TestPlannedRestartNoticeSetOnStopAndClearedWhenServing(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	announceRestart(store, node.ID, log)
	if m, err := store.ListComputeNodeVMMDRestartNotices(ctx); err != nil || m[node.ID].IsZero() {
		t.Fatalf("notice after stop = %v, %v", m, err)
	}
	endRestartNotice(ctx, store, node.ID, log)
	if m, err := store.ListComputeNodeVMMDRestartNotices(ctx); err != nil || len(m) != 0 {
		t.Fatalf("notice after serving = %v, %v", m, err)
	}
	announceRestart(nil, node.ID, log) // A storeless vmmd sets nothing.
}

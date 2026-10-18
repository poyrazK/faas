package e2etest

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

// destroyLiveVMs destroys every microVM the harness's own vmmd still runs,
// before that vmmd is stopped. vmmd leaves live VMs running when it exits so
// that a restarted vmmd can adopt them; in a test, the instance still warm at
// teardown outlived the test and failed leakcheck. procs are the daemons
// still to be stopped: when the harness did not start vmmd (an external
// FAAS_E2E_VMMD_SOCKET) or it has already exited, nothing is touched.
func (h *Harness) destroyLiveVMs(procs []*exec.Cmd) {
	running := slices.ContainsFunc(procs, func(p *exec.Cmd) bool {
		// A daemon may run under a setpriv prefix, so look at every argument.
		return p.Process != nil && p.ProcessState == nil &&
			slices.ContainsFunc(p.Args, func(arg string) bool { return filepath.Base(arg) == "vmmd" })
	})
	if !running || h.VMMDSock == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := wire.DialContext(ctx, h.VMMDSock, nil)
	if err != nil {
		h.T.Logf("e2etest: dial vmmd to destroy live VMs: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	client := vmmdpb.NewVmmdClient(conn)
	statsCtx, statsCancel := context.WithTimeout(ctx, 5*time.Second)
	stats, err := client.Stats(statsCtx, &vmmdpb.StatsRequest{})
	statsCancel()
	if err != nil {
		h.T.Logf("e2etest: list live VMs at teardown: %v", err)
		return
	}
	for _, instance := range stats.GetInstances() {
		destroyCtx, destroyCancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := client.Destroy(destroyCtx, &vmmdpb.DestroyRequest{Instance: instance.GetInstance()})
		destroyCancel()
		if err != nil {
			h.T.Logf("e2etest: destroy live VM %s at teardown: %v", instance.GetInstance(), err)
			continue
		}
		h.T.Logf("e2etest: destroyed live VM %s at teardown", instance.GetInstance())
	}
}

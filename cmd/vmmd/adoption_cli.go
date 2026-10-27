package main

// The adoption commands a release runs on a compute node (vmmd restart
// adoption ADR §4, slice 4), both against the running vmmd's admin socket:
//
//	vmmd adoption-check   run with the CANDIDATE binary before the restart:
//	                      would it adopt every live guest? Exit 0 yes, 3 no
//	                      (drain), 1 the check itself failed.
//	vmmd adoption-report  run after the restart: wait for the successor's
//	                      adoption report. Exit 0 for a complete, unfenced
//	                      report with zero refused, 3 otherwise (roll back),
//	                      1 when no report arrived in time.
//
// Both print one JSON object on stdout.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

const (
	adoptionExitOK     = 0
	adoptionExitError  = 1
	adoptionExitNotYet = 3
)

// runAdoptionCommand runs an adoption subcommand, or the rollout's
// base-generation-check (base_generation_cli.go) or base-adoption-check
// (base_adoption_cli.go); ok is false for any other
// invocation, which then starts the daemon as before.
func runAdoptionCommand(args []string, stdout, stderr io.Writer) (code int, ok bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "adoption-check":
		return adoptionCheckCommand(args[1:], stdout, stderr), true
	case "adoption-report":
		return adoptionReportCommand(args[1:], stdout, stderr), true
	case "base-generation-check":
		return baseGenerationCheckCommand(args[1:], stdout, stderr), true
	case "base-adoption-check":
		return baseAdoptionCheckCommand(args[1:], stdout, stderr), true
	}
	return 0, false
}

type adoptionCheckResult struct {
	AllAdoptable bool `json:"all_adoptable"`
	Records      int  `json:"records"`
	Adoptable    int  `json:"adoptable"`
	// Killed lists the app tasks and executions the restart will end; they
	// need no drain, which would end them too.
	Killed      []string               `json:"killed"`
	Unadoptable []adoptionCheckVerdict `json:"unadoptable"`
	// Reason is set when the check could not judge the guests at all.
	Reason string `json:"reason,omitempty"`
}

type adoptionCheckVerdict struct {
	Instance string `json:"instance"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}

func adoptionCheckCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("adoption-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", envOr("FAAS_VMMD_CONFIG", "/etc/faas/vmmd.toml"), "the candidate's vmmd.toml")
	socket := fs.String("socket", vmmdgrpc.AdoptionAdminSocket, "the running vmmd's admin socket")
	timeout := fs.Duration("timeout", time.Minute, "how long the running vmmd may take to prove its guests")
	if err := fs.Parse(args); err != nil {
		return adoptionExitError
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vmmd adoption-check: %v\n", err)
		return adoptionExitError
	}
	res := adoptionCheckResult{Killed: []string{}, Unadoptable: []adoptionCheckVerdict{}}
	switch {
	case cfg.NativeProcessRecovery:
		res.Reason = "native_process_recovery keeps its strict quarantine"
	case !cfg.AdoptionRecord || !cfg.AdoptRunningVMs:
		res.Reason = "the candidate's config does not adopt (adoption_record and adopt_running_vms)"
	default:
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		items, err := fetchAdoptionInventory(ctx, *socket)
		if err != nil {
			res.Reason = "the running vmmd did not report its inventory: " + err.Error()
			break
		}
		res.Records = len(items)
		for _, v := range fcvm.CandidateAdoptionVerdicts(items, fcvm.JailerChrootRoot, cfg.AdoptAdmittedRuntimes) {
			if v.Adoptable && v.Resolved == fcvm.AdoptionResolvedKilled {
				res.Killed = append(res.Killed, v.Instance)
				continue
			}
			if v.Adoptable {
				res.Adoptable++
				continue
			}
			res.Unadoptable = append(res.Unadoptable, adoptionCheckVerdict{Instance: v.Instance, Reason: v.Reason, Detail: v.Detail})
		}
	}
	res.AllAdoptable = res.Reason == "" && len(res.Unadoptable) == 0
	if err := json.NewEncoder(stdout).Encode(res); err != nil {
		return adoptionExitError
	}
	if !res.AllAdoptable {
		return adoptionExitNotYet
	}
	return adoptionExitOK
}

func fetchAdoptionInventory(ctx context.Context, socket string) ([]fcvm.AdoptionInventoryRecord, error) {
	conn, err := dialAdoptionAdmin(socket)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	resp, err := vmmdpb.NewVmmdClient(conn).AdoptionInventory(ctx, &vmmdpb.AdoptionInventoryRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]fcvm.AdoptionInventoryRecord, 0, len(resp.GetRecords()))
	for _, r := range resp.GetRecords() {
		out = append(out, fcvm.AdoptionInventoryRecord{Record: r.GetRecord(), ProofOK: r.GetProofOk(), ProofReason: r.GetProofReason(), ProofDetail: r.GetProofDetail()})
	}
	return out, nil
}

type adoptionReportResult struct {
	Clean       bool                   `json:"clean"`
	NodeID      string                 `json:"node_id,omitempty"`
	Epoch       uint64                 `json:"epoch,omitempty"`
	Fenced      bool                   `json:"fenced"`
	Complete    bool                   `json:"complete"`
	Adopted     int                    `json:"adopted"`
	Stopped     int                    `json:"stopped"`
	Killed      int                    `json:"killed"`
	Refused     []adoptionCheckVerdict `json:"refused"`
	Reason      string                 `json:"reason,omitempty"`
	WaitedMilli int64                  `json:"waited_ms"`
}

func adoptionReportCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("adoption-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", vmmdgrpc.AdoptionAdminSocket, "the restarted vmmd's admin socket")
	wait := fs.Duration("wait", 90*time.Second, "how long to wait for the report")
	if err := fs.Parse(args); err != nil {
		return adoptionExitError
	}
	started := time.Now()
	resp, err := waitAdoptionReport(*socket, *wait)
	res := adoptionReportResult{Refused: []adoptionCheckVerdict{}, WaitedMilli: time.Since(started).Milliseconds()}
	code := adoptionExitNotYet
	switch {
	case status.Code(err) == codes.Unimplemented:
		res.Reason = "the restarted vmmd does not adopt"
	case err != nil:
		res.Reason = "no adoption report: " + err.Error()
		code = adoptionExitError
	default:
		res.NodeID, res.Epoch, res.Fenced, res.Complete, res.Adopted = resp.GetNodeId(), resp.GetEpoch(), resp.GetFenced(), resp.GetComplete(), len(resp.GetAdopted())
		for _, r := range resp.GetRefused() {
			if r.GetReason() == "" && fcvm.AdoptionEnds(r.GetResolved()) {
				// A finished stop or a killed task, not a refusal.
				if r.GetResolved() == fcvm.AdoptionResolvedKilled {
					res.Killed++
				} else {
					res.Stopped++
				}
				continue
			}
			res.Refused = append(res.Refused, adoptionCheckVerdict{Instance: r.GetInstance(), Reason: r.GetReason(), Detail: r.GetDetail()})
		}
		res.Clean = !res.Fenced && res.Complete && len(res.Refused) == 0
		if res.Clean {
			code = adoptionExitOK
		}
	}
	if err := json.NewEncoder(stdout).Encode(res); err != nil {
		return adoptionExitError
	}
	return code
}

// waitAdoptionReport polls until the restarted vmmd serves its report: the
// socket appears once vmmd has finished adopting.
func waitAdoptionReport(socket string, wait time.Duration) (*vmmdpb.AdoptionReportResponse, error) {
	deadline := time.Now().Add(wait)
	for {
		resp, err := fetchAdoptionReport(socket)
		if err == nil || status.Code(err) == codes.Unimplemented || time.Now().After(deadline) {
			return resp, err
		}
		time.Sleep(time.Second)
	}
}

func fetchAdoptionReport(socket string) (*vmmdpb.AdoptionReportResponse, error) {
	if _, err := os.Stat(socket); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	conn, err := dialAdoptionAdmin(socket)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return vmmdpb.NewVmmdClient(conn).AdoptionReport(ctx, &vmmdpb.AdoptionReportRequest{})
}

func dialAdoptionAdmin(socket string) (*grpc.ClientConn, error) {
	if socket == "" {
		return nil, errors.New("no admin socket")
	}
	return grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

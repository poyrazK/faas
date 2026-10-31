package sched

// The vmmd restart adoption report (adoption ADR §3): what a restarted vmmd
// took over under its ownership epoch.

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// ErrAdoptionReportOff means the node's vmmd does not adopt on restart (an
// older binary, or adopt_running_vms off). There is nothing to apply.
var ErrAdoptionReportOff = errors.New("sched: vmmd restart adoption is off")

// AdoptionRefusal is why vmmd does not hold one guest: a refusal reason, or,
// with no reason, an interrupted stop it finished (AdoptionResolvedStopped)
// or an app task or execution it killed (AdoptionResolvedKilled).
type AdoptionRefusal struct {
	AppID, Reason, Detail, Resolved string
}

// AdoptionReport is one vmmd start's inventory. Leased holds every instance
// that vmmd gave a slot after startup: with Adopted, everything it holds or
// held. Complete is false once vmmd stopped tracking leases; such a report
// proves nothing about absence.
type AdoptionReport struct {
	NodeID                 string
	Epoch                  uint64
	Incarnation            string
	PredecessorIncarnation string
	Fenced                 bool
	Complete               bool
	Adopted                map[string]string // instance -> app
	// Resolved holds, for adopted instances, the outcome of the operation in
	// flight at the restart (AdoptionResolvedThawed).
	Resolved map[string]string
	Refused  map[string]AdoptionRefusal
	Leased   map[string]struct{}
	// Admissions holds, for adopted guests booted under an ADR-595 receipt,
	// the receipt the successor carries; schedd re-binds each admission to
	// the successor (docs/adr/20261018-runtime-admission-rebind.md).
	Admissions map[string]AdoptionAdmission
}

// AdoptionAdmission is one adopted guest's serving receipt and residency.
type AdoptionAdmission struct {
	Token               string
	IssuingIncarnation  string
	CompletedAtUnixNano int64
	Netns               string
	HostIP              string
	LeaseUID            int32
	Paused              bool
}

// Outcomes of an operation the restart cut short (vmmd restart adoption ADR
// §3). AdoptionResolvedThawed: a freeze or thaw; the restarted vmmd resumed
// the guest. AdoptionResolvedStopped: a stop, destroy, park or warm snapshot;
// it finished the teardown, so the guest is gone. AdoptionResolvedKilled: an
// app task or execution, whose stream ended with the predecessor; its
// coordinator reports the transport failure. vmmd also reports "resumed" for
// an unanswered migration prepare, which needs nothing here.
const (
	AdoptionResolvedThawed  = "thawed"
	AdoptionResolvedStopped = "stopped"
	AdoptionResolvedKilled  = "killed"
)

// Holds reports whether this vmmd adopted or created instance.
func (r AdoptionReport) Holds(instance string) bool {
	if _, ok := r.Adopted[instance]; ok {
		return true
	}
	_, ok := r.Leased[instance]
	return ok
}

func (c *VMMClient) AdoptionReport(ctx context.Context) (AdoptionReport, error) {
	resp, err := c.cli.AdoptionReport(ctx, &vmmdpb.AdoptionReportRequest{})
	if status.Code(err) == codes.Unimplemented {
		return AdoptionReport{}, ErrAdoptionReportOff
	}
	if err != nil {
		return AdoptionReport{}, liftErr(err)
	}
	return adoptionReportFromProto(resp)
}

// adoptionReportFromProto refuses a malformed inventory: an empty or repeated
// instance would make absence ambiguous.
func adoptionReportFromProto(resp *vmmdpb.AdoptionReportResponse) (AdoptionReport, error) {
	r := AdoptionReport{
		NodeID: resp.GetNodeId(), Epoch: resp.GetEpoch(), Incarnation: resp.GetIncarnation(),
		PredecessorIncarnation: resp.GetPredecessorIncarnation(), Fenced: resp.GetFenced(), Complete: resp.GetComplete(),
		Adopted: map[string]string{}, Resolved: map[string]string{}, Refused: map[string]AdoptionRefusal{}, Leased: map[string]struct{}{},
		Admissions: map[string]AdoptionAdmission{},
	}
	if r.NodeID == "" || r.Epoch == 0 || r.Incarnation == "" {
		return AdoptionReport{}, errors.New("sched: adoption report without node, epoch or incarnation")
	}
	seen := map[string]bool{}
	note := func(id string) error {
		if id == "" || seen[id] {
			return fmt.Errorf("sched: adoption report lists instance %q twice or empty", id)
		}
		seen[id] = true
		return nil
	}
	for _, a := range resp.GetAdopted() {
		if err := note(a.GetInstance()); err != nil {
			return AdoptionReport{}, err
		}
		r.Adopted[a.GetInstance()] = a.GetAppId()
		if res := a.GetResolved(); res != "" {
			r.Resolved[a.GetInstance()] = res
		}
		if adm := a.GetAdmission(); adm != nil {
			r.Admissions[a.GetInstance()] = AdoptionAdmission{
				Token: adm.GetToken(), IssuingIncarnation: adm.GetIssuingIncarnation(), CompletedAtUnixNano: adm.GetCompletedAtUnixNano(),
				Netns: adm.GetNetns(), HostIP: adm.GetHostIp(), LeaseUID: adm.GetLeaseUid(), Paused: adm.GetPaused(),
			}
		}
	}
	for _, a := range resp.GetRefused() {
		if err := note(a.GetInstance()); err != nil {
			return AdoptionReport{}, err
		}
		r.Refused[a.GetInstance()] = AdoptionRefusal{AppID: a.GetAppId(), Reason: a.GetReason(), Detail: a.GetDetail(), Resolved: a.GetResolved()}
	}
	for _, id := range resp.GetLeasedSinceStart() {
		if id == "" {
			return AdoptionReport{}, errors.New("sched: adoption report lists an empty lease")
		}
		// A refused survivor's instance may be woken again by the successor;
		// the lease then wins. An adopted one cannot be leased anew.
		if _, ok := r.Adopted[id]; ok {
			return AdoptionReport{}, fmt.Errorf("sched: adoption report leases adopted instance %q", id)
		}
		r.Leased[id] = struct{}{}
	}
	return r, nil
}

type adoptionReportClient interface {
	AdoptionReport(context.Context) (AdoptionReport, error)
}

// AdoptionReport fetches nodeID's adoption report through the router.
func (r *VMMRouter) AdoptionReport(ctx context.Context, nodeID string) (AdoptionReport, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return AdoptionReport{}, err
	}
	reporter, ok := cli.(adoptionReportClient)
	if !ok {
		return AdoptionReport{}, ErrAdoptionReportOff
	}
	rep, err := reporter.AdoptionReport(ctx)
	if err != nil {
		return AdoptionReport{}, err
	}
	if rep.NodeID != nodeID {
		return AdoptionReport{}, fmt.Errorf("sched: adoption report from node %q, asked %q", rep.NodeID, nodeID)
	}
	return rep, nil
}

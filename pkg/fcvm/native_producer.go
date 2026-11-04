package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"

	core "github.com/onebox-faas/faas/pkg/datarepair"
)

const (
	nativeRepairProducerProtocol = "data-repair-coldboot-v1"
	// nativeLaunchOwnerProtocol names the vmmd incarnation that published an
	// ordinary version-three launch record (ADR-2026101710 Amendment 1).
	nativeLaunchOwnerProtocol = "vmmd-launch-owner-v1"
)

// Version two enrolls only repair's fixed cold-boot path. Out-of-process
// producers use the existing gated helper/resource journals; in-process
// filesystem producers belong to this exact daemon kernel incarnation.
// Native builder export remains disabled. Version-one ownership is not upgraded.
// Version three gives an ordinary launch the same daemon identity, as its
// owner, so a later vmmd can prove that incarnation exited before cleanup.
type nativeProducerIdentity struct {
	Protocol  string `json:"protocol"`
	PID       int    `json:"pid"`
	UID       int    `json:"uid"`
	StartTime uint64 `json:"start_time"`
}

type nativeProducerContextKey struct{}

func (p *nativeProducerIdentity) UnmarshalJSON(b []byte) error {
	if _, err := nativeJournalObjectFields(b, []string{"protocol", "pid", "uid", "start_time"}); err != nil {
		return err
	}
	type plain nativeProducerIdentity
	return json.NewDecoder(bytes.NewReader(b)).Decode((*plain)(p))
}

func (p nativeProducerIdentity) validate() error {
	if p.Protocol != nativeRepairProducerProtocol && p.Protocol != nativeLaunchOwnerProtocol || p.PID <= 0 || p.PID > math.MaxInt32 || p.UID < 0 || uint64(p.UID) > math.MaxUint32 || p.StartTime == 0 {
		return errors.New("native producer: invalid daemon incarnation or protocol")
	}
	return nil
}

func (r nativeLaunchRecord) validateProducer() error {
	none := nativeProducerIdentity{}
	if r.Version == 1 && r.Producer == none && r.Owner == none {
		return nil
	}
	if r.Version == 3 && r.Producer == none && r.Owner.Protocol == nativeLaunchOwnerProtocol {
		return r.Owner.validate()
	}
	if r.Version != 2 || r.Owner != none || r.Producer.Protocol != nativeRepairProducerProtocol || !r.Lease.Networkless || r.Lease.IsBuilder || !canonicalNativeHelperID(r.Lease.Instance) {
		return errors.New("native producer: record version or enrolled workload is invalid")
	}
	return r.Producer.validate()
}

func nativeCurrentProducer() (nativeProducerIdentity, error) {
	return nativeCurrentIdentity(nativeRepairProducerProtocol)
}

func nativeCurrentIdentity(protocol string) (nativeProducerIdentity, error) {
	start, err := nativeHostHelperStartTime(os.Getpid())
	if err != nil {
		return nativeProducerIdentity{}, err
	}
	p := nativeProducerIdentity{Protocol: protocol, PID: os.Getpid(), UID: os.Geteuid(), StartTime: start}
	return p, p.validate()
}

func (r *nativeProcessRecoveryRuntime) repairProducerContext(ctx context.Context, lease Lease) (context.Context, error) {
	f, repair := ctx.Value(dataRepairContextKey{}).(core.RehearsalFrame)
	if !repair {
		return ctx, nil
	}
	if !nativeRepairProducerSupported() {
		return ctx, nil // Portable fixtures retain v1; native admission requires Linux pidfds.
	}
	if f.Validate() != nil || f.TaskID != lease.Instance || !lease.Networkless || lease.IsBuilder {
		return ctx, errors.New("native producer: repair enrollment differs from lease")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return ctx, err
	}
	p, err := nativeCurrentProducer()
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, nativeProducerContextKey{}, p), nil
}

// Each native launch/staging/child gate checks the original private repair
// frame. Compatibility methods without that context cannot borrow this owner.
func (j *nativeLaunchJournal) requireRepairProducerContext(ctx context.Context, owner nativeLaunchRecord) error {
	if owner.Producer == (nativeProducerIdentity{}) {
		return nil
	}
	f, ok := ctx.Value(dataRepairContextKey{}).(core.RehearsalFrame)
	r, err := readDataRepairRecord(filepath.Join(j.root, "data-repair", owner.Lease.Instance+".json"))
	if err != nil || !ok || !r.Frame.Same(f) || r.Revoked || r.NativeGeneration != owner.Generation || r.NativeProducer != owner.Producer || !sameNativePhysicalLease(r.NativeLease, owner.Lease) {
		return errors.New("native producer: repair frame has no original bound producer")
	}
	current, err := nativeCurrentProducer()
	if err != nil || current != owner.Producer {
		return errors.New("native producer: daemon incarnation changed")
	}
	return nil
}

// A root lock alone does not prove daemon death: an erroneous close must not
// authorize removing resources while an old filesystem writer can still run.
// Never signal the daemon or a PID that has since been reused.
func (v *JailerVMM) requireNativeProducerRetirement(ctx context.Context, record nativeLaunchRecord) error {
	if record.ResourcesRemoved {
		return nil
	}
	r := v.nativeRecovery
	if record.Producer != (nativeProducerIdentity{}) {
		if err := record.validateProducer(); err != nil {
			return err
		}
		parent, err := readDataRepairRecord(filepath.Join(r.journal.root, "data-repair", record.Lease.Instance+".json"))
		if err != nil || parent.Frame.TaskID != record.Lease.Instance || parent.NativeGeneration != "" && (parent.NativeGeneration != record.Generation || parent.NativeBootID != record.KernelBootID || parent.NativeProducer != record.Producer || !sameNativePhysicalLease(parent.NativeLease, record.Lease)) {
			return errors.New("native producer: original repair ownership is unavailable")
		}
		if err := r.checkDaemonOwnership(); err != nil {
			return err
		}
	}
	if r.generation(record.Lease.Instance) == record.Generation {
		if record.Producer != (nativeProducerIdentity{}) {
			current, err := nativeCurrentProducer()
			if err != nil || current != record.Producer {
				return errors.New("native producer: local producer identity changed")
			}
		}
		if record.Owner != (nativeProducerIdentity{}) {
			current, err := nativeCurrentIdentity(nativeLaunchOwnerProtocol)
			if err != nil || current != record.Owner {
				return errors.New("native owner: local daemon identity changed")
			}
		}
		return nil
	}
	probe := r.producerExited
	if probe == nil {
		probe = nativeProducerExited
	}
	if record.Producer == (nativeProducerIdentity{}) {
		if record.Owner == (nativeProducerIdentity{}) {
			return errors.New("native recovery: recovered host helper ownership is not yet attested")
		}
		// An earlier vmmd published this ordinary generation with its own
		// identity. Its in-process writers end with it; helpers keep their
		// journals. Prove it exited under the ownership lock, never signal it.
		if err := record.validateProducer(); err != nil {
			return err
		}
		if err := r.checkDaemonOwnership(); err != nil {
			return err
		}
		return probe(ctx, record.Owner)
	}
	return probe(ctx, record.Producer)
}

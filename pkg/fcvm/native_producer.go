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
	// nativeBuilderExportProducerProtocol enrolls every native builder. This
	// daemon incarnation reads the exited builder's drive and writes its
	// artifact export (ADR-20261022090000), so recovery must prove that writer
	// exited before it retires the export mount.
	nativeBuilderExportProducerProtocol = "builder-export-v1"
)

// Version two enrolls repair's fixed cold-boot path and native builders.
// Out-of-process producers use the existing gated helper/resource journals;
// in-process filesystem producers belong to this exact daemon kernel
// incarnation. Version-one ownership is not upgraded.
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
	if p.Protocol != nativeRepairProducerProtocol && p.Protocol != nativeBuilderExportProducerProtocol || p.PID <= 0 || p.PID > math.MaxInt32 || p.UID < 0 || uint64(p.UID) > math.MaxUint32 || p.StartTime == 0 {
		return errors.New("native producer: invalid daemon incarnation or protocol")
	}
	return nil
}

func (r nativeLaunchRecord) validateProducer() error {
	if r.Version == 1 && r.Producer == (nativeProducerIdentity{}) {
		return nil
	}
	if r.Version != 2 || r.Producer == (nativeProducerIdentity{}) {
		return errors.New("native producer: record version or enrolled workload is invalid")
	}
	switch r.Producer.Protocol {
	case nativeRepairProducerProtocol:
		if !r.Lease.Networkless || r.Lease.IsBuilder || !canonicalNativeHelperID(r.Lease.Instance) {
			return errors.New("native producer: record version or enrolled workload is invalid")
		}
	case nativeBuilderExportProducerProtocol:
		if !r.Lease.IsBuilder || r.Lease.Networkless || !validNativeInstanceName(r.Lease.Instance) {
			return errors.New("native producer: builder enrollment differs from its lease")
		}
	}
	return r.Producer.validate()
}

func nativeCurrentProducer(protocol string) (nativeProducerIdentity, error) {
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
	p, err := nativeCurrentProducer(nativeRepairProducerProtocol)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, nativeProducerContextKey{}, p), nil
}

// builderExportProducerContext enrolls this daemon incarnation as the producer
// of a native builder's artifact export. Every builder lease carries an export
// destination, so enrollment follows the lease alone.
func (r *nativeProcessRecoveryRuntime) builderExportProducerContext(ctx context.Context, lease Lease) (context.Context, error) {
	if !lease.IsBuilder || !nativeRepairProducerSupported() {
		return ctx, nil // Portable fixtures retain v1; native export then refuses.
	}
	if _, repair := ctx.Value(dataRepairContextKey{}).(core.RehearsalFrame); repair || lease.Networkless {
		return ctx, errors.New("native producer: a builder cannot borrow repair enrollment")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return ctx, err
	}
	p, err := nativeCurrentProducer(nativeBuilderExportProducerProtocol)
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
	if owner.Producer.Protocol == nativeBuilderExportProducerProtocol {
		// A builder has no repair frame; only its enrolling incarnation may act.
		current, err := nativeCurrentProducer(owner.Producer.Protocol)
		if err != nil || current != owner.Producer {
			return errors.New("native producer: daemon incarnation changed")
		}
		return nil
	}
	f, ok := ctx.Value(dataRepairContextKey{}).(core.RehearsalFrame)
	r, err := readDataRepairRecord(filepath.Join(j.root, "data-repair", owner.Lease.Instance+".json"))
	if err != nil || !ok || !r.Frame.Same(f) || r.Revoked || r.NativeGeneration != owner.Generation || r.NativeProducer != owner.Producer || !sameNativePhysicalLease(r.NativeLease, owner.Lease) {
		return errors.New("native producer: repair frame has no original bound producer")
	}
	current, err := nativeCurrentProducer(owner.Producer.Protocol)
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
		if record.Producer.Protocol == nativeRepairProducerProtocol {
			parent, err := readDataRepairRecord(filepath.Join(r.journal.root, "data-repair", record.Lease.Instance+".json"))
			if err != nil || parent.Frame.TaskID != record.Lease.Instance || parent.NativeGeneration != "" && (parent.NativeGeneration != record.Generation || parent.NativeBootID != record.KernelBootID || parent.NativeProducer != record.Producer || !sameNativePhysicalLease(parent.NativeLease, record.Lease)) {
				return errors.New("native producer: original repair ownership is unavailable")
			}
		}
		if err := r.checkDaemonOwnership(); err != nil {
			return err
		}
	}
	if r.generation(record.Lease.Instance) == record.Generation {
		if record.Producer != (nativeProducerIdentity{}) {
			current, err := nativeCurrentProducer(record.Producer.Protocol)
			if err != nil || current != record.Producer {
				return errors.New("native producer: local producer identity changed")
			}
		}
		return nil
	}
	if record.Producer == (nativeProducerIdentity{}) {
		return errors.New("native recovery: recovered host helper ownership is not yet attested")
	}
	probe := r.producerExited
	if probe == nil {
		probe = nativeProducerExited
	}
	return probe(ctx, record.Producer)
}

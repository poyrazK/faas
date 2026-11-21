package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// The flight is persisted before Wake. It classifies only a fresh, private
// template launch; physical retirement still requires the native launch owner.
// Snapshot objects are never deleted by recovery: their catalog commit may be
// uncertain, and imaged owns that retention decision.
type executionTemplateFlight struct {
	Version      int                    `json:"version"`
	Instance     string                 `json:"instance"`
	Memory       string                 `json:"memory"`
	KernelBootID string                 `json:"kernel_boot_id"`
	Owner        nativeProducerIdentity `json:"owner"`
}

type executionTemplateFlightContextKey struct{}

func (f *executionTemplateFlight) UnmarshalJSON(body []byte) error {
	if _, err := nativeJournalObjectFields(body, []string{"version", "instance", "memory", "kernel_boot_id", "owner"}); err != nil {
		return err
	}
	type plain executionTemplateFlight
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(f))
}

func (f executionTemplateFlight) validate() error {
	if f.Version != 1 || !canonicalNativeHelperID(f.Instance) || !canonicalNativeHelperID(f.KernelBootID) || !executionTemplateMemoryKey(f.Memory) || f.Owner.Protocol != nativeLaunchOwnerProtocol {
		return errors.New("execution template: invalid durable flight")
	}
	return f.Owner.validate()
}

func (j *nativeLaunchJournal) executionTemplateRoot() string {
	return filepath.Join(j.root, "runtime-captures", "templates")
}
func (j *nativeLaunchJournal) executionTemplatePath(instance string) (string, error) {
	if !canonicalNativeHelperID(instance) {
		return "", errors.New("execution template: noncanonical instance")
	}
	return filepath.Join(j.executionTemplateRoot(), instance+".json"), nil
}
func (j *nativeLaunchJournal) readExecutionTemplate(instance string) (flight executionTemplateFlight, result error) {
	path, err := j.executionTemplatePath(instance)
	if err != nil {
		return flight, err
	}
	if err := checkNativeJournalPath(j.executionTemplateRoot(), true); err != nil {
		return flight, err
	}
	if err := checkNativeJournalPath(path, false); err != nil {
		return flight, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return flight, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	decoder := json.NewDecoder(io.LimitReader(file, api.NativeSnapshotPublicationRecordMaxBytes+1))
	if err := decoder.Decode(&flight); err != nil {
		return flight, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return flight, errors.New("execution template: trailing flight data")
	}
	if err := flight.validate(); err != nil {
		return flight, err
	}
	boot, err := j.currentBootID()
	if err != nil || flight.Instance != instance || flight.KernelBootID != boot {
		return flight, errors.Join(err, errors.New("execution template: flight identity or kernel boot changed"))
	}
	return flight, nil
}

// No caller can borrow a reserved template UUID through an ordinary Wake.
func (j *nativeLaunchJournal) requireExecutionTemplateLaunch(ctx context.Context, lease Lease) error {
	expected, template := ctx.Value(executionTemplateFlightContextKey{}).(executionTemplateFlight)
	if !canonicalNativeHelperID(lease.Instance) && !template {
		return nil
	}
	flight, err := j.readExecutionTemplate(lease.Instance)
	if errors.Is(err, os.ErrNotExist) && !template {
		return nil
	}
	if err != nil {
		return err
	}
	owner, err := nativeCurrentIdentity(nativeLaunchOwnerProtocol)
	if err != nil || !template || flight != expected || flight.Owner != owner || !j.owners || !lease.Networkless || lease.IsBuilder || lease.Plan != api.PlanScale {
		return errors.Join(err, errors.New("execution template: original private launch flight required"))
	}
	return ctx.Err()
}

func (v *JailerVMM) beginExecutionTemplateFlight(ctx context.Context, instance, memory string) (context.Context, error) {
	if v.nativeRecovery == nil {
		return ctx, nil
	}
	r := v.nativeRecovery
	if !v.nativeRuntimeCaptureEnabled() || !r.journal.owners {
		return ctx, errors.New("execution template: native capture and launch owner records required")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return ctx, err
	}
	j := r.journal
	owner, err := nativeCurrentIdentity(nativeLaunchOwnerProtocol)
	if err != nil {
		return ctx, err
	}
	boot, err := j.currentBootID()
	if err != nil {
		return ctx, err
	}
	flight := executionTemplateFlight{Version: 1, Instance: instance, Memory: memory, KernelBootID: boot, Owner: owner}
	if err := flight.validate(); err != nil {
		return ctx, err
	}
	if _, err := j.read(instance); !errors.Is(err, os.ErrNotExist) {
		return ctx, errors.Join(err, errors.New("execution template: UUID already has a physical owner"))
	}
	path, err := j.executionTemplatePath(instance)
	if err != nil {
		return ctx, err
	}
	if err := os.MkdirAll(j.executionTemplateRoot(), 0o700); err != nil {
		return ctx, err
	}
	if err := checkNativeJournalPath(j.executionTemplateRoot(), true); err != nil {
		return ctx, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return ctx, errors.Join(err, errors.New("execution template: flight namespace already exists"))
	}
	if err := writeNativeJournalValue(path, flight); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, executionTemplateFlightContextKey{}, flight), nil
}

func (j *nativeLaunchJournal) removeExecutionTemplate(expected executionTemplateFlight) error {
	actual, err := j.readExecutionTemplate(expected.Instance)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("execution template: flight changed before retirement")
	}
	physical, err := j.read(expected.Instance)
	if err == nil {
		if physical.Version != 3 || physical.Owner != expected.Owner || physical.KernelBootID != expected.KernelBootID || !physical.Lease.Networkless || physical.Lease.IsBuilder || physical.Lease.Plan != api.PlanScale || !physical.ResourcesRemoved || !physical.ExitConfirmed || !physical.Revoked {
			return errors.New("execution template: physical retirement is not confirmed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	path, err := j.executionTemplatePath(expected.Instance)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncResourceParent(path)
}

func (v *JailerVMM) finishExecutionTemplateFlight(ctx context.Context, instance, memory string) error {
	if v.nativeRecovery == nil {
		return nil
	}
	j := v.nativeRecovery.journal
	flight, err := j.readExecutionTemplate(instance)
	if err != nil {
		return err
	}
	expected, ok := ctx.Value(executionTemplateFlightContextKey{}).(executionTemplateFlight)
	if !ok || flight != expected || memory != flight.Memory {
		return errors.New("execution template: original flight completion required")
	}
	if err := v.nativeRecovery.checkDaemonOwnership(); err != nil {
		return err
	}
	return j.removeExecutionTemplate(flight)
}

// Called inside the Manager's startup gate, before allocating any fresh lease.
// The initial native inventory has already checked every physical owner. No
// recovered template enters live/readiness or obtains capture replay authority.
func (v *JailerVMM) recoverExecutionTemplateFlights(ctx context.Context, cleanup func(context.Context, Lease) error) (bool, error) {
	r := v.nativeRecovery
	if r == nil {
		return false, nil
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return false, err
	}
	j := r.journal
	entries, err := os.ReadDir(j.executionTemplateRoot())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := checkNativeJournalPath(j.executionTemplateRoot(), true); err != nil {
		return false, err
	}
	changed := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !entry.Type().IsRegular() {
			return changed, errors.New("execution template: unexpected flight entry")
		}
		flight, err := j.readExecutionTemplate(id)
		if err != nil {
			return changed, err
		}
		probe := r.producerExited
		if probe == nil {
			probe = nativeProducerExited
		}
		if err := probe(ctx, flight.Owner); err != nil {
			return changed, fmt.Errorf("execution template: original producer still owns flight: %w", err)
		}
		physical, err := j.read(id)
		if err == nil {
			if physical.Version != 3 || physical.Owner != flight.Owner || physical.KernelBootID != flight.KernelBootID || !physical.Lease.Networkless || physical.Lease.IsBuilder || physical.Lease.Plan != api.PlanScale {
				return changed, errors.New("execution template: recovered physical owner differs from flight")
			}
			if err := cleanup(ctx, physical.Lease); err != nil {
				return changed, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return changed, err
		}
		// Retained incomplete runtime starts/intents are audit evidence only. Their
		// objects stay under catalog-aware imaged retention, including lost ACKs.
		if err := j.removeExecutionTemplate(flight); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

func nativeExecutionTemplateCheckpoint(ctx context.Context, stage string, lease Lease) error {
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if !ok || permit.Runtime.Version != 2 {
		return nil
	}
	return executionTemplateCaptureCheckpoint(ctx, stage, lease.Instance, permit.Runtime.Keys.Memory)
}

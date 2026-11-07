// adr: 149 — amendment 2026-11-07: prepared networks under native recovery.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// nativePreparedSpareVersion is the record version that carries a prepared
// link: version three (it names its vmmd owner) plus the link. vmmd writes it
// only while native_prepared_networks is on; every reader accepts it.
const nativePreparedSpareVersion = 4

// nativePreparedLink ties a prepared network spare to the instance that
// claimed it. A spare's own record names itself as Source and leaves Target
// empty until a claim commits. The claiming instance's record names the spare
// as Source, with the spare's generation, until the spare is archived.
type nativePreparedLink struct {
	Source           string `json:"source"`
	Target           string `json:"target"`
	SourceGeneration string `json:"source_generation"`
}

func nativePreparedLinkFields() []string {
	return []string{"source", "target", "source_generation"}
}

// nativePreparedSpareName reports whether instance names a prepared spare:
// "prepared-" and a canonical UUID, as the cache has always named them.
func nativePreparedSpareName(instance string) bool {
	id, ok := strings.CutPrefix(instance, "prepared-")
	return ok && canonicalNativeHelperID(id)
}

// spare reports whether r is a prepared spare's own record.
func (r nativeLaunchRecord) spare() bool {
	return r.Prepared != (nativePreparedLink{}) && r.Lease.Instance == r.Prepared.Source
}

func (r nativeLaunchRecord) validatePrepared() error {
	link := r.Prepared
	if (r.Version == nativePreparedSpareVersion) != (link != nativePreparedLink{}) {
		return errors.New("native prepared network: record version requires its link")
	}
	if link == (nativePreparedLink{}) {
		return nil
	}
	if !nativePreparedSpareName(link.Source) || link.Target != "" && (!validNativeInstanceName(link.Target) || nativePreparedSpareName(link.Target)) {
		return errors.New("native prepared network: link names an invalid spare or target")
	}
	if r.Lease.Networkless || r.Lease.IsBuilder {
		return errors.New("native prepared network: link requires a tenant network lease")
	}
	if r.spare() {
		// A spare never launches: it holds host network objects only.
		if link.SourceGeneration != "" || r.Authorized || r.PID != 0 {
			return errors.New("native prepared network: spare record carries launch state")
		}
		return nil
	}
	if r.Lease.Instance != link.Target || !canonicalNativeHelperID(link.SourceGeneration) {
		return errors.New("native prepared network: target record differs from its link")
	}
	return nil
}

func decodeNativePreparedLink(raw json.RawMessage) error {
	if _, err := nativeJournalObjectFields(raw, nativePreparedLinkFields()); err != nil {
		return fmt.Errorf("native prepared network: link fields: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var link nativePreparedLink
	return decoder.Decode(&link)
}

type nativePreparedClaimKey struct{}

// withNativePreparedClaim marks a lease preparation as the target of a
// committed spare claim, so its record carries the link.
func withNativePreparedClaim(ctx context.Context, link nativePreparedLink) context.Context {
	return context.WithValue(ctx, nativePreparedClaimKey{}, link)
}

func nativePreparedClaimFrom(ctx context.Context) (nativePreparedLink, bool) {
	link, ok := ctx.Value(nativePreparedClaimKey{}).(nativePreparedLink)
	return link, ok
}

// prepareSpare publishes a spare's record before any of its host effects. The
// spare is an unlaunched record for its own pseudo-instance, so its helpers
// are fenced by the same lock, generation and kernel boot as a VM launch.
func (j *nativeLaunchJournal) prepareSpare(ctx context.Context, lease Lease) (record nativeLaunchRecord, err error) {
	if !j.preparedSpares || !j.owners {
		return record, errors.New("native prepared network: spares require native_prepared_networks and launch owner records")
	}
	if !nativePreparedSpareName(lease.Instance) {
		return record, errors.New("native prepared network: spare has no prepared identity")
	}
	if err := validateNativeJournalLease(lease); err != nil {
		return record, err
	}
	bootID, err := j.currentBootID()
	if err != nil {
		return record, err
	}
	lock, err := j.lock(ctx, lease.Instance)
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if _, statErr := os.Lstat(j.path(lease.Instance)); !errors.Is(statErr, os.ErrNotExist) {
		if statErr != nil {
			return record, statErr
		}
		return record, fmt.Errorf("native prepared network: spare %s already has a record", lease.Instance)
	}
	record, err = j.owned(nativeLaunchRecord{Version: 1, Generation: uuid.NewString(), KernelBootID: bootID, Lease: lease})
	if err != nil {
		return record, err
	}
	record.Version, record.Prepared = nativePreparedSpareVersion, nativePreparedLink{Source: lease.Instance}
	return record, j.write(record)
}

// claimSpare commits expected's claim by target before the claim moves any
// host object. Afterwards no helper or second claim can act for the spare.
func (j *nativeLaunchJournal) claimSpare(ctx context.Context, expected nativeLaunchRecord, target string) (record nativeLaunchRecord, err error) {
	if !validNativeInstanceName(target) || nativePreparedSpareName(target) {
		return record, errors.New("native prepared network: invalid claim target")
	}
	lock, err := j.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, err = j.read(expected.Lease.Instance)
	if err != nil {
		return record, err
	}
	if !record.spare() || record.Generation != expected.Generation || record.KernelBootID != expected.KernelBootID ||
		!sameNativePhysicalLease(record.Lease, expected.Lease) || record.Prepared.Target != "" || record.Revoked || record.ResourcesRemoved {
		return record, errors.New("native prepared network: spare is not claimable")
	}
	record.Prepared.Target = target
	return record, j.write(record)
}

// linkPreparedTarget adds a committed claim's link to record, the claiming
// instance's new launch record. The spare must already name it, on the same
// slot, so the two records hold one set of host objects between them.
func (j *nativeLaunchJournal) linkPreparedTarget(record nativeLaunchRecord, link nativePreparedLink) (nativeLaunchRecord, error) {
	if !j.preparedSpares || record.Version != 3 || link.Target != record.Lease.Instance {
		return record, errors.New("native prepared network: claim link needs a version-three target record")
	}
	spare, err := j.read(link.Source)
	if err != nil {
		return record, fmt.Errorf("native prepared network: read claimed spare: %w", err)
	}
	if !spare.spare() || spare.Prepared.Target != link.Target || spare.Generation != link.SourceGeneration || spare.Revoked || spare.ResourcesRemoved ||
		spare.Lease.Slot != record.Lease.Slot || spare.Lease.UID != record.Lease.UID || spare.Lease.HostIP != record.Lease.HostIP {
		return record, errors.New("native prepared network: claimed spare does not name this target")
	}
	record.Version, record.Prepared = nativePreparedSpareVersion, link
	return record, nil
}

// withPreparedTarget runs effect, a host-object move of a claimed spare, while
// holding the target's launch lock. Only the target's current local generation
// that still links the spare, and has not launched, may run it.
func (j *nativeLaunchJournal) withPreparedTarget(ctx context.Context, target, generation string, effect func(nativeLaunchRecord) error) (err error) {
	lock, err := j.lock(ctx, target)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, err := j.read(target)
	if err != nil {
		return err
	}
	if record.Generation != generation || record.Prepared.Source == "" || record.spare() || record.Authorized || record.Revoked {
		return errors.New("native prepared network: target no longer owns the claimed spare")
	}
	return effect(record)
}

// finishClaim archives a claimed spare, whose host objects now belong to its
// target, and then publishes the target as an ordinary version-three record.
// Each half is idempotent, so recovery can finish an interrupted claim.
func (j *nativeLaunchJournal) finishClaim(ctx context.Context, link nativePreparedLink) error {
	if err := j.archiveClaimedSpare(ctx, link); err != nil {
		return err
	}
	return j.unlinkPreparedTarget(ctx, link)
}

func (j *nativeLaunchJournal) archiveClaimedSpare(ctx context.Context, link nativePreparedLink) (err error) {
	lock, err := j.lock(ctx, link.Source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	spare, err := j.read(link.Source)
	if errors.Is(err, os.ErrNotExist) {
		return nil // already archived
	}
	if err != nil {
		return err
	}
	if !spare.spare() || spare.Prepared.Target != link.Target || spare.Generation != link.SourceGeneration {
		return errors.New("native prepared network: archived spare differs from its claim")
	}
	helpers := nativeHostHelperJournal{owner: j}
	if err := helpers.requireRetired(spare); err != nil {
		return err
	}
	// The spare launched nothing, and its objects moved to the target.
	spare.Revoked, spare.ExitConfirmed, spare.ResourcesRemoved = true, true, true
	archive := filepath.Join(j.root, "retired")
	if err := os.MkdirAll(archive, 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(archive, true); err != nil {
		return err
	}
	path := filepath.Join(archive, spare.Generation+".json")
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		if err := writeNativeLaunchRecord(path, spare); err != nil {
			return fmt.Errorf("native prepared network: archive claimed spare: %w", err)
		}
	} else if statErr != nil {
		return statErr
	} else if archived, err := readNativeLaunchRecord(path, spare.Lease.Instance); err != nil || !sameArchivedSpare(archived, spare) {
		return errors.Join(errors.New("native prepared network: spare archive differs from its claim"), err)
	}
	if err := os.Remove(j.path(link.Source)); err != nil {
		return err
	}
	return syncNativeJournalDir(j.root)
}

func (j *nativeLaunchJournal) unlinkPreparedTarget(ctx context.Context, link nativePreparedLink) (err error) {
	lock, err := j.lock(ctx, link.Target)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, err := j.read(link.Target)
	if err != nil {
		return err
	}
	if record.Prepared == (nativePreparedLink{}) {
		return nil // already ordinary
	}
	if record.Prepared != link {
		return errors.New("native prepared network: target links another spare")
	}
	record.Version, record.Prepared = 3, nativePreparedLink{}
	return j.write(record)
}

// sameArchivedSpare lets a retried archive accept only the identical frame.
func sameArchivedSpare(a, b nativeLaunchRecord) bool {
	return a.Version == b.Version && a.Generation == b.Generation && a.KernelBootID == b.KernelBootID && a.Prepared == b.Prepared &&
		a.Owner == b.Owner && a.Revoked && a.ExitConfirmed && a.ResourcesRemoved && !a.Authorized && sameNativePhysicalLease(a.Lease, b.Lease)
}

func syncNativeJournalDir(path string) (err error) {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return dir.Sync()
}

// nativePreparedState is how recovery treats a spare's record.
type nativePreparedState int

const (
	// nativePreparedReady is an unclaimed spare: recovery retires it.
	nativePreparedReady nativePreparedState = iota + 1
	// nativePreparedAbandoned is claimed, but its target never published a
	// linked record: the spare still owns its objects and is retired.
	nativePreparedAbandoned
	// nativePreparedTransferred is claimed by a target whose record links it:
	// the target owns the slot, and recovery finishes the claim.
	nativePreparedTransferred
)

// classifyNativePrepared sorts every spare record of a journal scan. A target
// that links a spare which names another target is corrupt and refuses
// recovery; a target whose spare is already archived needs only its link
// dropped, which finishClaim does.
func classifyNativePrepared(records []nativeLaunchRecord) (map[string]nativePreparedState, error) {
	byID := make(map[string]nativeLaunchRecord, len(records))
	for _, r := range records {
		byID[r.Lease.Instance] = r
	}
	out := make(map[string]nativePreparedState)
	for _, r := range records {
		if r.Prepared == (nativePreparedLink{}) {
			continue
		}
		if !r.spare() {
			if spare, ok := byID[r.Prepared.Source]; ok && (spare.Prepared.Target != r.Lease.Instance || spare.Generation != r.Prepared.SourceGeneration) {
				return nil, fmt.Errorf("native prepared network: %s links spare %s, which it never claimed", r.Lease.Instance, r.Prepared.Source)
			}
			continue
		}
		switch target, ok := byID[r.Prepared.Target]; {
		case r.Prepared.Target == "":
			out[r.Lease.Instance] = nativePreparedReady
		case ok && target.Prepared.Source == r.Lease.Instance && target.Prepared.SourceGeneration == r.Generation:
			out[r.Lease.Instance] = nativePreparedTransferred
		default:
			out[r.Lease.Instance] = nativePreparedAbandoned
		}
	}
	return out, nil
}

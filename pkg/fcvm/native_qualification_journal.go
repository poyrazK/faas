package fcvm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This journal binds incoming authority to one immutable physical generation.
// Binding and revocation alone never grant a retirement or readiness receipt.
type nativeQualificationRecord struct {
	Version          int                                     `json:"version"`
	Generation       string                                  `json:"generation"`
	KernelBootID     string                                  `json:"kernel_boot_id"`
	Execution        state.EnvironmentQualificationExecution `json:"execution"`
	CleanupToken     string                                  `json:"cleanup_token"`
	AcceptedAt       time.Time                               `json:"accepted_at"`
	Deadline         time.Time                               `json:"deadline"`
	CreateStarted    bool                                    `json:"create_started"`
	Revoked          bool                                    `json:"revoked"`
	NativeGeneration string                                  `json:"native_generation"`
	NativeLease      Lease                                   `json:"native_lease"`
}

func (r nativeQualificationRecord) String() string {
	return fmt.Sprintf("%s native_attempt=%s started=%t revoked=%t", r.Execution.String(), r.Generation, r.CreateStarted, r.Revoked)
}
func (r nativeQualificationRecord) GoString() string { return r.String() }

func nativeQualificationJSONFields(t reflect.Type) []string {
	var fields []string
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		// The v2 capture journal predates restore authority. Its original wire
		// shape remains exact; a restore frame requires a separate protocol.
		if t == reflect.TypeOf(state.EnvironmentQualificationExecution{}) && name == "capture_instance_id" {
			continue
		}
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	return fields
}

func (r *nativeQualificationRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, nativeQualificationJSONFields(reflect.TypeOf(*r)))
	if err != nil {
		return err
	}
	execution, err := nativeJournalObjectFields(fields["execution"], nativeQualificationJSONFields(reflect.TypeOf(r.Execution)))
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(execution["artifact"], nativeQualificationJSONFields(reflect.TypeOf(r.Execution.Artifact))); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["native_lease"], nativeJournalLeaseFields()); err != nil {
		return err
	}
	type plain nativeQualificationRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*plain)(r)); err != nil {
		return err
	}
	r.Execution.CleanupToken = r.CleanupToken
	return nil
}

func nativeQualificationUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && (id.String() == value || strings.ReplaceAll(id.String(), "-", "") == value)
}

func validateNativeQualificationFrame(frame state.EnvironmentQualificationExecution, nodeID string) error {
	if frame.CaptureInstanceID != "" {
		return errors.New("native qualification: capture protocol cannot own a restore target")
	}
	for _, value := range []string{frame.InstanceID, frame.RequestID, frame.GraphID, frame.AppID, frame.DeploymentID, frame.NodeID,
		frame.WakeID, frame.SourceID, frame.EnvironmentID, frame.RevisionID, frame.CleanupToken} {
		if !nativeQualificationUUID(value) {
			return errors.New("native qualification: incomplete original execution identity")
		}
	}
	name, workload := strings.CutPrefix(frame.Resource, "workload/")
	digest, digestErr := hex.DecodeString(frame.PlanHash)
	if frame.NodeID != nodeID || !workload || !api.ValidAppSlug(name) || !api.ValidProjectEnvironmentSlug(frame.Scope) ||
		frame.Generation < 1 || frame.IntentVersion < 0 || frame.Attempt < 1 || frame.RAMMB <= 0 || digestErr != nil || len(digest) != 32 || hex.EncodeToString(digest) != frame.PlanHash ||
		frame.Artifact.RootfsBytes <= 0 || frame.Artifact.RootfsKey == "" && frame.Artifact.RootfsPath == "" {
		return errors.New("native qualification: original node, environment or artifact authority is incomplete")
	}
	switch frame.Artifact.Kind {
	case state.DeploymentKindImage, state.DeploymentKindTarball, state.DeploymentKindDockerfile, state.DeploymentKindGitHub, state.DeploymentKindPreview:
		return nil
	default:
		return errors.New("native qualification: unsupported original artifact kind")
	}
}

func (r nativeQualificationRecord) validate(nodeID string) error {
	if err := validateNativeQualificationFrame(r.Execution, nodeID); err != nil {
		return err
	}
	if r.Version != 2 || !canonicalNativeHelperID(r.Generation) || !canonicalNativeHelperID(r.KernelBootID) || r.CleanupToken != r.Execution.CleanupToken || r.AcceptedAt.IsZero() ||
		r.CreateStarted && (!r.AcceptedAt.Before(r.Deadline) || r.Deadline.Sub(r.AcceptedAt) > api.EnvironmentGitOpsQualificationMaxLeaseDuration) || !r.CreateStarted && (!r.Revoked || !r.Deadline.IsZero()) {
		return errors.New("native qualification: invalid incoming request state")
	}
	if r.NativeGeneration == "" {
		if r.NativeLease != (Lease{}) {
			return errors.New("native qualification: physical lease has no generation")
		}
	} else if !r.CreateStarted || !canonicalNativeHelperID(r.NativeGeneration) || r.NativeGeneration == r.Generation ||
		validateNativeQualificationLease(r.Execution, r.NativeLease) != nil {
		return errors.New("native qualification: invalid original physical binding")
	}
	return nil
}

type nativeQualificationJournal struct {
	root       string
	nodeID     string
	owner      *nativeLaunchJournal
	now        func() time.Time
	writeValue func(string, nativeQualificationRecord) error
}

func (j *nativeQualificationJournal) clock() time.Time {
	if j.now != nil {
		return j.now()
	}
	return time.Now()
}

// One canonical file key prevents alternate UUID spellings from creating
// parallel request records. The exact original spelling remains in the frame.
func (j *nativeQualificationJournal) key(instance string) (string, error) {
	if !nativeQualificationUUID(instance) {
		return "", errors.New("native qualification: invalid instance identity")
	}
	id, _ := uuid.Parse(instance)
	return id.String(), nil
}

func (j *nativeQualificationJournal) lock(ctx context.Context, instance string) (*os.File, error) {
	key, err := j.key(instance)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(j.root, 0o700); err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return nil, err
	}
	return lockNativeJournalFile(ctx, filepath.Join(j.root, key+".lock"))
}

func (j *nativeQualificationJournal) path(instance string) (string, error) {
	key, err := j.key(instance)
	return filepath.Join(j.root, key+".json"), err
}

func (j *nativeQualificationJournal) read(instance string) (record nativeQualificationRecord, result error) {
	path, err := j.path(instance)
	if err != nil {
		return record, err
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return record, err
	}
	if err := checkNativeJournalPath(path, false); err != nil {
		return record, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return record, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, errors.New("native qualification: trailing record data")
	}
	nodeID := j.nodeID
	if nodeID == "" {
		// Inventory validates the frame without claiming local dispatch authority.
		nodeID = record.Execution.NodeID
	}
	if err := record.validate(nodeID); err != nil {
		return record, err
	}
	key, _ := j.key(record.Execution.InstanceID)
	want, _ := j.key(instance)
	bootID, err := j.owner.currentBootID()
	if err != nil || key != want || record.KernelBootID != bootID {
		return record, errors.Join(err, errors.New("native qualification: incoming record belongs to another instance or kernel boot"))
	}
	return record, nil
}

func (j *nativeQualificationJournal) write(record nativeQualificationRecord) error {
	if err := record.validate(j.nodeID); err != nil {
		return err
	}
	path, err := j.path(record.Execution.InstanceID)
	if err != nil {
		return err
	}
	if j.writeValue != nil {
		return j.writeValue(path, record)
	}
	return writeNativeJournalValue(path, record)
}

func (j *nativeQualificationJournal) update(ctx context.Context, frame state.EnvironmentQualificationExecution, create bool) (record nativeQualificationRecord, result error) {
	if j.owner == nil {
		return record, errors.New("native qualification: original native journal unavailable")
	}
	if err := validateNativeQualificationFrame(frame, j.nodeID); err != nil {
		return record, err
	}
	lock, err := j.lock(ctx, frame.InstanceID)
	if err != nil {
		return record, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	record, err = j.read(frame.InstanceID)
	if err == nil {
		if record.Execution != frame || create {
			return record, errors.New("native qualification: original incoming request already exists or changed")
		}
		if record.Revoked {
			return record, j.revokeNative(ctx, record)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		bootID, err := j.owner.currentBootID()
		if err != nil {
			return record, err
		}
		if create {
			if err := j.requireNativeAbsent(frame.InstanceID); err != nil {
				return record, err
			}
		}
		record = nativeQualificationRecord{Version: 2, Generation: uuid.NewString(), KernelBootID: bootID, Execution: frame, CleanupToken: frame.CleanupToken, AcceptedAt: j.clock().UTC()}
	} else {
		return record, err
	}
	if create {
		deadline, ok := ctx.Deadline()
		if !ok || !record.AcceptedAt.Before(deadline) || deadline.Sub(record.AcceptedAt) > api.EnvironmentGitOpsQualificationMaxLeaseDuration {
			return record, errors.New("native qualification: original bounded dispatch deadline is missing or expired")
		}
		record.Deadline, record.CreateStarted = deadline, true
	} else {
		record.Revoked = true
	}
	if err := ctx.Err(); err != nil {
		return record, err
	}
	if err := j.write(record); err != nil {
		return record, err
	}
	if !create {
		return record, j.revokeNative(ctx, record)
	}
	return record, nil
}

func (j *nativeQualificationJournal) claim(ctx context.Context, frame state.EnvironmentQualificationExecution) (nativeQualificationRecord, error) {
	return j.update(ctx, frame, true)
}

func (j *nativeQualificationJournal) revoke(ctx context.Context, frame state.EnvironmentQualificationExecution) (nativeQualificationRecord, error) {
	return j.update(ctx, frame, false)
}

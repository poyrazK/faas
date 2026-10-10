// adr: 568 — append-only receipts belong to one immutable original capture intent.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeSnapshotPublicationObjectReceipt struct {
	Version    int                              `json:"version"`
	Directory  nativeLoopIdentity               `json:"directory"`
	File       nativeLoopIdentity               `json:"file"`
	IntentFile nativeLoopIdentity               `json:"intent_file"`
	CaptureID  string                           `json:"capture_id"`
	Kind       string                           `json:"kind"`
	Object     storage.ExclusiveArtifactReceipt `json:"object"`
}

func (r *nativeSnapshotPublicationObjectReceipt) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"version", "directory", "file", "intent_file", "capture_id", "kind", "object"})
	if err != nil {
		return err
	}
	for _, name := range []string{"directory", "file", "intent_file"} {
		if _, err := nativeJournalObjectFields(fields[name], []string{"device", "inode"}); err != nil {
			return err
		}
	}
	type plain nativeSnapshotPublicationObjectReceipt
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode((*plain)(r))
}

func nativePublicationObjectKey(intent nativeSnapshotPublicationIntent, kind string) string {
	switch kind {
	case "mem":
		return intent.Keys.Memory
	case "vmstate":
		return intent.Keys.VMState
	case "drive":
		return intent.Keys.Drive
	case "backing":
		return intent.Keys.Backing
	default:
		return ""
	}
}

func (r nativeSnapshotPublicationObjectReceipt) validate(intent nativeSnapshotPublicationIntent) error {
	if err := errors.Join(intent.validate(), r.Object.Validate()); err != nil {
		return err
	}
	if r.Version != 1 || r.Directory != intent.Directory || r.IntentFile != intent.File || r.File.Device != intent.Directory.Device || r.File.Inode == 0 || r.File == intent.File || r.File.Inode == r.Directory.Inode || r.CaptureID != intent.Capture.CaptureID || r.Object.Key != nativePublicationObjectKey(intent, r.Kind) || r.Object.Key == "" {
		return errors.New("native snapshot publication: object receipt differs from original intent")
	}
	return nil
}

type nativeSnapshotPublicationReceiptJournal interface {
	RecordObject(context.Context, nativeSnapshotPublicationIntent, string, storage.ExclusiveArtifactReceipt) (nativeSnapshotPublicationObjectReceipt, error)
	RequireObject(context.Context, nativeSnapshotPublicationIntent, nativeSnapshotPublicationObjectReceipt) error
}

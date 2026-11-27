package pgarchive

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgexport"
)

const (
	UploadTimeout       = 30 * time.Minute
	UploadComplete byte = 1
	Owner               = "gregale_schema_owner"
)

type Request struct {
	TargetDatabase string   `json:"target_database"`
	OperationID    string   `json:"operation_id"`
	Manifest       Manifest `json:"manifest"`
}

func (r Request) Valid() bool {
	return pgexport.ValidDatabase(r.TargetDatabase) && dbguest.ValidOperationID(r.OperationID) && r.Manifest.Valid()
}

type Reply struct {
	Ready  bool             `json:"ready_for_upload,omitempty"`
	Result dbguest.Response `json:"result"`
}

func ReadLine(reader *bufio.Reader, value any) error {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return err
	}
	if len(line) > ManifestLimit {
		return fmt.Errorf("archive metadata exceeds the size limit")
	}
	return json.Unmarshal(line, value)
}

func ConfirmUpload(reader io.Reader) error {
	var complete [1]byte
	if _, err := io.ReadFull(reader, complete[:]); err != nil || complete[0] != UploadComplete {
		return ErrArchive
	}
	return nil
}

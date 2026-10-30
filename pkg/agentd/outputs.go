package agentd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// A succeeded run's output files reach agentd inline in its result (ADR-20261027090010
// §3). OutputFiles keeps their contents in the artifact store, keyed by
// account, run and digest, before the run is finished; the manifest records
// each key. The outputs endpoint and bucket deliveries read them from there.

// outcomeOutputsUnavailable fails a run whose files could not be kept.
const outcomeOutputsUnavailable = "outputs_unavailable"

// outputPutAttempts bounds the writes of one file.
const outputPutAttempts = 3

// OutputFiles stores output file contents.
type OutputFiles struct {
	Storage storage.StorageBackend
}

// OutputFileKey is where a run's file with digest sum is kept.
func OutputFileKey(accountID, runID, sum string) string {
	return "agents/outputs/" + accountID + "/" + runID + "/" + strings.TrimPrefix(sum, "sha256:")
}

// Store writes each file and returns its manifest entry with the key. It
// checks each file's size and digest first, so the manifest describes the
// bytes kept.
func (o *OutputFiles) Store(ctx context.Context, accountID, runID string, files []agentrunproto.OutputFile) ([]agentstore.OutputFile, error) {
	out := make([]agentstore.OutputFile, 0, len(files))
	for _, f := range files {
		sum := sha256.Sum256(f.Content)
		if len(f.Content) != f.Size || f.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf("agentd: output %q does not match its size or digest", f.Name)
		}
		key := OutputFileKey(accountID, runID, f.SHA256)
		var err error
		for attempt := range outputPutAttempts {
			if attempt > 0 && !sleep(ctx, time.Duration(attempt)*time.Second) {
				return nil, ctx.Err()
			}
			if err = o.Storage.Put(ctx, key, bytes.NewReader(f.Content)); err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("agentd: keep output %q: %w", f.Name, err)
		}
		out = append(out, agentstore.OutputFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256, Key: key})
	}
	return out, nil
}

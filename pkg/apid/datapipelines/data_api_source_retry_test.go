package datapipelines

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

// Retain a valid archive from a different template release, without depending
// on git history or changing the worker's embedded source during a test.
func retainEarlierDataAPITemplate(t *testing.T, path string) {
	t.Helper()
	input, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	output, err := os.Create(path + ".previous")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	zip := gzip.NewWriter(output)
	writer, reader := tar.NewWriter(zip), tar.NewReader(gz)
	changed := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "data-api/Dockerfile" {
			const previous = "# Previous worker template release\n"
			header.Size += int64(len(previous))
			if err = writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(writer, previous); err != nil {
				t.Fatal(err)
			}
			changed = true
		} else if err = writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err = io.Copy(writer, reader); err != nil {
			t.Fatal(err)
		}
	}
	if !changed {
		t.Fatal("template Dockerfile absent")
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = zip.Close(); err != nil {
		t.Fatal(err)
	}
	if err = output.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path+".previous", path); err != nil {
		t.Fatal(err)
	}
}

func TestDataAPISetupSourceChangeRetriesOnlyUnacceptedIntent(t *testing.T) {
	setup, store, job := apiSetupFixture(t)
	ctx := t.Context()
	binding, ready, err := setup.Prepare(ctx, job)
	if err != nil || !ready {
		t.Fatal(binding, ready, err)
	}
	job.DataAPIPlan.BindingID = binding
	path, _, err := setup.source(job)
	if err != nil {
		t.Fatal(err)
	}
	retainEarlierDataAPITemplate(t, path)
	saved, err := sourceDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	deployment, _ := apidsource.IntentIDs(job.DataAPIPlan.IntentID, job.DataAPIPlan.AppID)
	for range 2 {
		if _, _, err = setup.Deploy(ctx, job); !errors.Is(err, datapipeline.ErrDataAPISourceChanged) || !errors.Is(err, datapipeline.ErrConflict) {
			t.Fatal("unaccepted archive change did not request explicit source retry", err)
		}
	}
	if _, err = store.DeploymentByID(ctx, deployment); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("conflicting source was admitted", err)
	}
	job.DataAPIPlan.IntentID = uuid.NewString() // Persisted by setup's explicit retry stage.
	accepted, build, err := setup.Deploy(ctx, job)
	if err != nil || accepted == deployment || build == "" {
		t.Fatal("fresh intent did not recover after upgrade", accepted, build, err)
	}
	if current, err := sourceDigest(path); err != nil || current != saved {
		t.Fatal("retry overwrote the preceding intent's archive", err)
	}
	// A lost accepted response always recovers the same IDs, independent of
	// later local archive trouble or template changes.
	currentPath, _, err := setup.source(job)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(currentPath, []byte("unavailable retained archive"), 0644); err != nil {
		t.Fatal(err)
	}
	replayed, replayedBuild, err := setup.Deploy(ctx, job)
	if err != nil || replayed != accepted || replayedBuild != build || setup.Bindings.(*setupBindings).creates != 1 {
		t.Fatal("accepted work or stable binding was replaced", replayed, replayedBuild, err)
	}
}

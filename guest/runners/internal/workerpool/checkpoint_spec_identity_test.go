// adr: 590 — target workers must not inherit captured source specifications.
package workerpool

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type checkpointSpecResponse struct {
	Value string `json:"value"`
	PID   int    `json:"pid"`
}

func TestCheckpointSpecProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_CHECKPOINT_SPEC_HELPER") != "1" {
		return
	}
	frozen := checkpointSpecResponse{
		Value: os.Getenv("GREGALE_CHECKPOINT_SPEC_TEST_VALUE"),
		PID:   os.Getpid(),
	}
	fmt.Fprintln(os.Stdout, "{\"__faas_ready\":true}")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if err := json.NewEncoder(os.Stdout).Encode(frozen); err != nil {
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func checkpointSpecFixture(t *testing.T) Spec {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "handler")
	if err := os.WriteFile(marker, []byte("# "+protocolMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestCheckpointSpecProcessHelper$"},
		Env: append(os.Environ(),
			"GO_WANT_CHECKPOINT_SPEC_HELPER=1",
			"GREGALE_CHECKPOINT_SPEC_TEST_VALUE=source",
		),
		HandlerPath: marker,
	}
}

func TestCheckpointSpecReplacementUsesTargetEnvironmentAfterRecycle(t *testing.T) {
	Recycle()
	t.Cleanup(func() { Recycle() })
	source := checkpointSpecFixture(t)
	if handled, err := PrewarmIfSupported(t.Context(), source); err != nil || !handled {
		t.Fatal("source initialization failed", err)
	}
	var captured checkpointSpecResponse
	if handled, err := InvokeIfSupported(t.Context(), source, struct{}{}, &captured); err != nil || !handled || captured.Value != "source" {
		t.Fatal("source process did not retain initialization values", err)
	}

	target := source
	target.Env = append([]string(nil), source.Env...)
	target.Env[len(target.Env)-1] = "GREGALE_CHECKPOINT_SPEC_TEST_VALUE=target"
	if count := Recycle(); count < 1 {
		t.Fatal("source worker was not recycled")
	}
	if handled, err := PrewarmIfSupported(t.Context(), target); err != nil || !handled {
		t.Fatal("target initialization failed", err)
	}
	for range 2 {
		var got checkpointSpecResponse
		if handled, err := InvokeIfSupported(t.Context(), target, struct{}{}, &got); err != nil || !handled || got.Value != "target" || got.PID == captured.PID {
			t.Fatal("replacement reused captured source configuration", err)
		}
	}
}

func TestCheckpointSpecPoolOwnsStartupSlices(t *testing.T) {
	spec := checkpointSpecFixture(t)
	p := newPool(spec)
	t.Cleanup(func() { p.recycle() })

	// Mutation after pool creation must not change the command or environment
	// used by a later initialization in the retained pool.
	spec.Args[0] = "-test.run=^NoCheckpointSpecProcessHelper$"
	spec.Env[len(spec.Env)-1] = "GREGALE_CHECKPOINT_SPEC_TEST_VALUE=changed"
	var got checkpointSpecResponse
	if err := p.invoke(t.Context(), struct{}{}, &got); err != nil || got.Value != "source" {
		t.Fatal("caller mutation changed retained startup specification", err)
	}
}

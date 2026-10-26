package imaged

// adr: 20261024191249, 20261026181418 — the rollout pre-stage publishes every
// runtime base the candidate configures, whoever owns the apps on it, under
// the generation's own keys.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestReleaseBasePlan(t *testing.T) {
	gated := GatedRuntimeBases()
	verdicts := func(stale ...string) map[string]string {
		out := make(map[string]string, len(gated))
		for _, runtime := range gated {
			out[runtime] = BaseAdoptionPublished
		}
		for _, runtime := range stale {
			out[runtime] = BaseAdoptionStale
		}
		return out
	}
	cases := []struct {
		name     string
		handled  map[string]bool
		verdicts map[string]string
		judgeErr error
		want     ReleaseBasesResult
		wantErr  bool
	}{
		// rc.293: fsn-2 owned no app, so the assigned pass staged nothing
		// and three bases still named the previous generation.
		{name: "rc.293: node owns no app, three bases changed",
			verdicts: verdicts("", RuntimePython312, RuntimeNode24),
			want: ReleaseBasesResult{
				Staged:  []string{"", RuntimePython312, RuntimeNode24},
				Current: []string{RuntimeNode22, RuntimeGo124, RuntimeGo124Alpine, RuntimePython313},
			}},
		{name: "the assigned pass already staged its bases",
			handled:  map[string]bool{"": true, RuntimeNode24: true},
			verdicts: verdicts("", RuntimePython312, RuntimeNode24),
			want: ReleaseBasesResult{
				Staged:  []string{RuntimePython312},
				Current: []string{RuntimeNode22, RuntimeGo124, RuntimeGo124Alpine, RuntimePython313},
			}},
		{name: "a later node finds everything published", verdicts: verdicts(),
			want: ReleaseBasesResult{Current: gated}},
		{name: "cached and unpublished bases are left alone",
			verdicts: map[string]string{"": BaseAdoptionCached, RuntimeNode22: BaseAdoptionUnpublished, RuntimePython312: BaseAdoptionStale,
				RuntimeGo124: BaseAdoptionUnpublished, RuntimeGo124Alpine: BaseAdoptionUnpublished, RuntimeNode24: BaseAdoptionPublished, RuntimePython313: BaseAdoptionUnpublished},
			want: ReleaseBasesResult{
				Staged:      []string{RuntimePython312},
				Current:     []string{"", RuntimeNode24},
				Unpublished: []string{RuntimeNode22, RuntimeGo124, RuntimeGo124Alpine, RuntimePython313},
			}},
		// ADR-20261026181418: a node on a release before generation keys can
		// restage the logical key before this node first boots the base.
		{name: "a generation published only under the legacy key is published under its own",
			verdicts: map[string]string{"": BaseAdoptionLegacy, RuntimeNode22: BaseAdoptionPublished, RuntimePython312: BaseAdoptionLegacy,
				RuntimeGo124: BaseAdoptionCached, RuntimeGo124Alpine: BaseAdoptionUnpublished, RuntimeNode24: BaseAdoptionStale, RuntimePython313: BaseAdoptionPublished},
			want: ReleaseBasesResult{
				Staged:      []string{"", RuntimePython312, RuntimeNode24},
				Current:     []string{RuntimeNode22, RuntimeGo124, RuntimePython313},
				Unpublished: []string{RuntimeGo124Alpine},
			}},
		{name: "an unreadable shared store fails the pre-stage", verdicts: verdicts(), judgeErr: errors.New("registry unavailable"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := releaseBasePlan(gated, tc.handled, func(runtime string) (BaseAdoption, error) {
				if tc.judgeErr != nil {
					return BaseAdoption{}, tc.judgeErr
				}
				return BaseAdoption{Verdict: tc.verdicts[runtime]}, nil
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("plan = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestStageReleaseBasesDefersOnlyLegacyParentInPrestage(t *testing.T) {
	legacy := fmt.Errorf("imaged: stage runtime base node24: %w", ErrVerifiedParentUnsupported)
	cases := []struct {
		name         string
		deferLegacy  bool
		fail         map[string]error
		wantDeferred []string
		wantStaged   []string
		wantErr      bool
	}{
		{name: "stages minimal and runtimes in order", wantStaged: []string{"", RuntimeNode24, RuntimePython312}},
		{name: "prestage defers a legacy parent and keeps going", deferLegacy: true, fail: map[string]error{RuntimeNode24: legacy},
			wantDeferred: []string{RuntimeNode24}, wantStaged: []string{"", RuntimeNode24, RuntimePython312}},
		{name: "any other failure is fatal", deferLegacy: true, fail: map[string]error{"": errors.New("grype timed out")},
			wantStaged: []string{""}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var staged []string
			deferred, err := stageReleaseBases([]string{"", RuntimeNode24, RuntimePython312}, tc.deferLegacy, func(runtime string) error {
				staged = append(staged, runtime)
				return tc.fail[runtime]
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(deferred, tc.wantDeferred) || !reflect.DeepEqual(staged, tc.wantStaged) {
				t.Fatalf("deferred = %v staged = %v; want %v %v", deferred, staged, tc.wantDeferred, tc.wantStaged)
			}
		})
	}
}

// The rc.293 failure end to end: the shared minimal base names the previous
// generation, the node owns no app, and the pre-stage must publish the
// candidate's generation anyway. The next node to roll builds nothing.
func TestEnsureReleaseBasesPublishesUnownedChangedBase(t *testing.T) {
	h, store, _, runner, ref := verifiedBaseFixture(t, "complete")
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	be, err := h.storageFor()
	if err != nil {
		t.Fatal(err)
	}
	const arch = "amd64"
	baseKey := storagekey.BaseForArch("", arch)
	previous := "registry.example/base@sha256:" + strings.Repeat("0", 64)
	if err := be.Put(t.Context(), baseKey, bytes.NewReader([]byte("previous generation"))); err != nil {
		t.Fatal(err)
	}
	if err := be.Put(t.Context(), baseKey+".digest", strings.NewReader("sha256:previous")); err != nil {
		t.Fatal(err)
	}
	if err := be.Put(t.Context(), wire.ScanKeyForBaseKey(baseKey), strings.NewReader(scanSidecarNaming(previous))); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FAAS_DEPLOY_BASE_REF_MINIMAL": ref}
	lookup := func(key string) string { return env[key] }

	if before, err := JudgeBaseAdoption(t.Context(), be, baseKey, ref); err != nil || before.Verdict != BaseAdoptionStale {
		t.Fatalf("fixture: minimal base %+v, %v; want stale", before, err)
	}
	result, err := h.EnsureReleaseBases(t.Context(), arch, lookup, AssignedBasesResult{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{""}; !reflect.DeepEqual(result.Staged, want) {
		t.Fatalf("staged = %q, want %q", result.Staged, want)
	}
	// The other runtimes resolve to refs nothing published in this store.
	if len(result.Unpublished) != len(GatedRuntimeBases())-1 || len(result.Current) != 0 {
		t.Fatalf("result = %+v, want every other runtime unpublished", result)
	}
	after, err := JudgeBaseAdoption(t.Context(), be, baseKey, ref)
	if err != nil || after.Verdict != BaseAdoptionPublished {
		t.Fatalf("after the pre-stage: %+v, %v; want published", after, err)
	}
	producer, err := store.GetCurrentBaseImageProducer(t.Context(), baseKey)
	if err != nil {
		t.Fatal(err)
	}

	built := len(runner.argv)
	runner.argv = nil
	again, err := h.EnsureReleaseBases(t.Context(), arch, lookup, AssignedBasesResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Staged) != 0 || !reflect.DeepEqual(again.Current, []string{""}) {
		t.Fatalf("second pre-stage = %+v, want the minimal base current and nothing staged", again)
	}
	if current, err := store.GetCurrentBaseImageProducer(t.Context(), baseKey); err != nil || current.ID != producer.ID || len(runner.argv) != 0 || built == 0 {
		t.Fatalf("second pre-stage rebuilt the base: producer %v→%v, mkfs %v", producer.ID, current.ID, runner.argv)
	}
}

// publishLog records every Put a staging run makes, in order.
type publishLog struct {
	storage.StorageBackend
	puts []publishedObject
}

type publishedObject struct{ key, body string }

func (l *publishLog) Put(ctx context.Context, key string, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	l.puts = append(l.puts, publishedObject{key: key, body: string(body)})
	return l.StorageBackend.Put(ctx, key, bytes.NewReader(body))
}

func (l *publishLog) LocalPath(key string) (string, bool, error) {
	return l.StorageBackend.(storage.LocalPathResolver).LocalPath(key)
}

// vmmd's ensureBaseGeneration reads the scan sidecar as the commit marker and
// only then pulls the base and its digest sidecar. A rebuild must therefore
// never publish a scan naming the new generation before the new base and
// digest are in place, or a node on the new release commits the previous
// bytes as the new generation.
func TestRebuildPublishesTheScanCommitMarkerLast(t *testing.T) {
	for _, rebuild := range []string{"first publication", "new guest-init"} {
		t.Run(rebuild, func(t *testing.T) {
			h, _, _, _, ref := verifiedBaseFixture(t, "complete")
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
			const key, digestKey = "base/base-amd64.ext4", "base/base-amd64.ext4.digest"
			if rebuild == "new guest-init" {
				if _, err := h.EnsureBaseExt4(t.Context(), ref, key, digestKey, "", "", ""); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(h.guestInitPath, []byte("NEWER INIT"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			be, err := h.storageFor()
			if err != nil {
				t.Fatal(err)
			}
			log := &publishLog{StorageBackend: be}
			h.WithStorage(log)
			if _, err := h.EnsureBaseExt4(t.Context(), ref, key, digestKey, "", "", ""); err != nil {
				t.Fatal(err)
			}
			lastBase, lastDigest, firstNaming := -1, -1, -1
			for i, put := range log.puts {
				switch put.key {
				case key:
					lastBase = i
				case digestKey:
					lastDigest = i
				case wire.ScanKeyForBaseKey(key):
					var scan struct{ Image string }
					if json.Unmarshal([]byte(put.body), &scan) == nil && scan.Image == ref && firstNaming < 0 {
						firstNaming = i
					}
				}
			}
			if lastBase < 0 || lastDigest < 0 || firstNaming < 0 {
				t.Fatalf("staging published base %d digest %d scan %d: %+v", lastBase, lastDigest, firstNaming, log.puts)
			}
			if firstNaming < lastBase || firstNaming < lastDigest {
				t.Fatalf("scan named %s at put %d, before the base (%d) or digest (%d) was published", ref, firstNaming, lastBase, lastDigest)
			}
		})
	}
}

package objectnode

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScrubFindingPrecedesProgressAndSurvivesFault(t *testing.T) {
	for _, boundary := range []string{"finding", "progress", "uncertain progress"} {
		t.Run(boundary, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "original")
			if err := os.WriteFile(scrubObjectPath(t, s, "key"), []byte("damaged!"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.StartScrub(t.Context()); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected checkpoint failure")
			s.scrubPersist = func(path string, value any) (bool, error) {
				if boundary == "finding" && strings.Contains(path, string(filepath.Separator)+"findings"+string(filepath.Separator)) {
					return false, injected
				}
				if boundary != "finding" && filepath.Base(path) == "progress.json" {
					if boundary == "uncertain progress" {
						published, err := writeJSON(path, value)
						if err != nil {
							return published, err
						}
						return published, injected
					}
					return false, injected
				}
				return writeJSON(path, value)
			}
			if _, err := s.ScrubStep(t.Context(), 32); !errors.Is(err, injected) {
				t.Fatal("fault boundary was not exercised", err)
			}
			before, err := s.ScrubFindings(t.Context(), "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "finding" && len(before.Items) != 0 || boundary != "finding" && len(before.Items) != 1 {
				t.Fatal("progress outran its durable finding", before)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			status := finishTestScrub(t, reopened, 32)
			after, err := reopened.ScrubFindings(t.Context(), "", 10)
			if err != nil || status.Failures != 1 || status.Processed != 1 || len(after.Items) != 1 || !after.Items[0].Current {
				t.Fatal("replay lost or duplicated a finding", status, after, err)
			}
			if len(before.Items) != 0 && !after.Items[0].FirstDetectedAt.Equal(before.Items[0].FirstDetectedAt) {
				t.Fatal("replay replaced first detection evidence")
			}
		})
	}
}

func TestScrubKilledCheckpointResumes(t *testing.T) {
	if directory := os.Getenv("GREGALE_SCRUB_CRASH_TEST"); directory != "" {
		s, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartScrub(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ScrubStep(context.Background(), 256); err != nil {
			t.Fatal(err)
		}
		_, _ = os.Stdout.WriteString("scrub checkpoint durable\n")
		select {}
	}
	s := testStore(t)
	putText(t, s, "key", strings.Repeat("a", 1024))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScrubKilledCheckpointResumes$")
	command.Env = append(os.Environ(), "GREGALE_SCRUB_CRASH_TEST="+s.root)
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	if scanner := bufio.NewScanner(out); !scanner.Scan() || scanner.Text() != "scrub checkpoint durable" {
		t.Fatal("child did not reach a durable checkpoint")
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	before, err := reopened.ScrubStatus()
	if err != nil || before.CurrentOffset != 256 {
		t.Fatal(before, err)
	}
	status := finishTestScrub(t, reopened, 256)
	if status.BytesRead != 1024 || status.Verified != 1 || status.Failures != 0 {
		t.Fatal("process kill restarted or lost the completed prefix", status)
	}
}

func TestScrubCorruptCheckpointAndManifestFailClosed(t *testing.T) {
	for _, target := range []string{"progress", "manifest", "finding"} {
		t.Run(target, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "original")
			status, err := s.StartScrub(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			path := s.scrubPath("progress.json")
			if target == "manifest" {
				path = s.scrubPath(status.RunID + ".jsonl")
			}
			if target == "finding" {
				if err = os.WriteFile(scrubObjectPath(t, s, "key"), []byte("damaged!"), 0o600); err != nil {
					t.Fatal(err)
				}
				finishTestScrub(t, s, 32)
				findings, err := s.ScrubFindings(t.Context(), "", 10)
				if err != nil {
					t.Fatal(err)
				}
				path = s.scrubPath("findings/" + findings.Items[0].ID + ".json")
			}
			if err = os.WriteFile(path, []byte("corrupt evidence"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if reopened, err := Open(s.root); !errors.Is(err, ErrCorrupt) {
				if reopened != nil {
					_ = reopened.Close()
				}
				t.Fatal("damaged progress became a clean scan", err)
			}
		})
	}
}

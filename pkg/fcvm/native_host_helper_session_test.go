//go:build linux || darwin

package fcvm

// adr: 783
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

func TestNativeHelperSessionPolicyAcknowledgementRefusesMissingOrChanged(t *testing.T) {
	for _, test := range []struct {
		name  string
		value []byte
		valid bool
	}{{"exact", []byte{1}, true}, {"empty", nil, false}, {"unsupported", []byte{2}, false}, {"extra", []byte{1, 1}, false}} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if _, err := writer.Write(test.value); err != nil {
				t.Fatal(err)
			}
			_ = writer.Close()
			if err := awaitNativeHelperSessionPolicy(t.Context(), reader); (err == nil) != test.valid {
				t.Fatal("invalid policy negotiation", err)
			}
		})
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := awaitNativeHelperSessionPolicy(ctx, reader); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled policy negotiation stalled", err)
	}
}

func TestNativeHelperSessionStatusRefusesMissingDuplicateAndUnrestricted(t *testing.T) {
	for _, value := range []string{"", "NoNewPrivs: 1\n", "Seccomp: 2\n", "NoNewPrivs: 0\nSeccomp: 2\n", "NoNewPrivs: 1\nSeccomp: 0\n", "NoNewPrivs: 1\nSeccomp: 2\nSeccomp: 2\n", "NoNewPrivs: 01\nSeccomp: 2\n"} {
		if err := nativeHelperSessionStatus(value); err == nil {
			t.Fatal("unrestricted or ambiguous kernel status accepted", value)
		}
	}
	if err := nativeHelperSessionStatus("Name: helper\nNoNewPrivs:\t1\nSeccomp:\t2\n"); err != nil {
		t.Fatal(err)
	}
}

type nativeRestrictedSessionFixture struct{ *nativeHelperGroupsFixture }

func (g nativeRestrictedSessionFixture) SessionRestricted(int, uint64) error {
	return errors.New("independent kernel restriction unavailable")
}

func TestNativeHelperSessionPolicyKernelCheckPrecedesExecution(t *testing.T) {
	j, owner, groups := nativeHelperJournalFixture(t)
	j.groups = nativeRestrictedSessionFixture{groups}
	cmd, marker := nativeHelperFixtureCommand(t)
	if err := j.run(t.Context(), owner, cmd, time.Second); err == nil {
		t.Fatal("unverified kernel policy opened execution gate")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("command ran without original session restriction", err)
	}
}

func TestNativeHelperLegacySessionCannotSupplyRestrictionAfterExit(t *testing.T) {
	j, owner, _ := nativeHelperJournalFixture(t)
	frame := nativeHelperTaskRecord(owner, 42, 100)
	frame.SessionPolicy = 0 // ADR-782 retained sessions did not enforce affiliation.
	frame.Launch.Revoked, frame.Launch.ExitConfirmed, frame.Launch.ResourcesRemoved = true, true, true
	writeNativeHelperTaskFrame(t, j, owner, frame)
	if err := j.retire(t.Context(), owner, frame.Launch.Generation); err == nil {
		t.Fatal("legacy session disappearance invented restriction")
	}
	if err := j.requireRemoved(t.Context(), owner); err == nil {
		t.Fatal("legacy session authorized replacement")
	}
	raw, _ := json.Marshal(frame)
	var got nativeHostHelperRecord
	if json.Unmarshal(raw, &got) != nil || got.validate(owner) != nil || got.SessionPolicy != 0 {
		t.Fatal("legacy shape became unreadable or policy backfilled")
	}
	frame.SessionPolicy = jailsetup.HostHelperSessionPolicyVersion
	if err := frame.validate(owner); err != nil {
		t.Fatal(err)
	}
}

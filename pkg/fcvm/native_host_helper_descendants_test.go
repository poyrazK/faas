//go:build linux || darwin

package fcvm

// adr: 782
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func nativeHelperFamilyProc(t *testing.T, root string, record nativeHostHelperRecord, pid, parent, session, uid int, start uint64, group string) string {
	t.Helper()
	frame := record
	frame.Launch.PID, frame.Launch.StartTime = pid, start
	dir := nativeHelperTaskProc(t, root, frame, uid, group)
	raw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		t.Fatal(err)
	}
	end := strings.LastIndexByte(string(raw), ')')
	fields := strings.Fields(string(raw[end+1:]))
	fields[1], fields[2], fields[3] = strconv.Itoa(parent), strconv.Itoa(session), strconv.Itoa(session)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(string(raw[:end+1])+" "+strings.Join(fields, " ")), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNativeHelperFamilyIncludesEscapedDescendantsAndOrphanSession(t *testing.T) {
	_, owner, _ := nativeHelperJournalFixture(t)
	frame := nativeHelperTaskRecord(owner, 42, 100)
	frame.SessionID = 42
	root := t.TempDir()
	nativeHelperFamilyProc(t, root, frame, 42, 1, 42, 0, 100, frame.Group.Path)
	nativeHelperFamilyProc(t, root, frame, 43, 42, 42, 1000, 101, "escaped")
	nativeHelperFamilyProc(t, root, frame, 44, 43, 555, 2000, 102, "other.slice")
	nativeHelperFamilyProc(t, root, frame, 45, 1, 42, 3000, 103, "outside")
	nativeHelperFamilyProc(t, root, frame, 46, 1, 42, 0, 99, "unrelated")
	probe := nativeProcessProbe{root: root}
	tasks, err := probe.helperTasks(t.Context(), []nativeHostHelperRecord{frame})
	if err != nil || len(tasks) != 4 || tasks[2].Relation != "observed_descendant" || tasks[3].Relation != "original_session_candidate" || tasks[3].LeaderPID != 42 || tasks[3].LeaderStartTime != 100 {
		t.Fatal("family escaped observation", tasks, err)
	}
	if len(unresolvedNativeHelperTasks(tasks)) != 4 {
		t.Fatal("family granted authority")
	}
	// An unrelated reused leader PID cannot adopt the old session survivors.
	nativeHelperFamilyProc(t, root, frame, 42, 1, 900, 0, 200, "unrelated")
	tasks, err = probe.helperTasks(t.Context(), []nativeHostHelperRecord{frame})
	if err != nil || len(tasks) != 3 || tasks[0].PID != 43 || tasks[1].PID != 44 || tasks[2].PID != 45 {
		t.Fatal("old session disappeared with original leader", tasks, err)
	}
	clone := cloneNativeRuntimeInventory(NativeRuntimeInventory{HelperTasks: tasks})
	clone.HelperTasks[0].ParentPID++
	if reflect.DeepEqual(clone.HelperTasks, tasks) {
		t.Fatal("family payload aliases source")
	}
}

func TestNativeHelperFamilyFindsLegacyAndUnpublishedCgroupOccupants(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		t.Run(strconv.FormatBool(authorized), func(t *testing.T) {
			_, owner, _ := nativeHelperJournalFixture(t)
			frame := nativeHelperTaskRecord(owner, 42, 100)
			frame.SessionID, frame.SessionPolicy = 0, 0 // Actual historical frames have no private-session provenance.
			frame.Launch.Authorized = authorized
			if !authorized {
				frame.Launch.PID, frame.Launch.StartTime = 0, 0
			}
			root := t.TempDir()
			nativeHelperFamilyProc(t, root, frame, 43, 1, 777, 1000, 101, frame.Group.Path+"/nested")
			tasks, err := (nativeProcessProbe{root: root}).helperTasks(t.Context(), []nativeHostHelperRecord{frame})
			if err != nil || len(tasks) != 1 || tasks[0].Relation != "original_cgroup_candidate" {
				t.Fatal("unrecorded group occupant hidden", tasks, err)
			}
		})
	}
}

func TestNativeHelperFamilyObservesLiteralSystemdCgroupEscapes(t *testing.T) {
	_, owner, _ := nativeHelperJournalFixture(t)
	frame := nativeHelperTaskRecord(owner, 42, 100)
	root := t.TempDir()
	group := `system.slice/system-serial\x2dgetty.slice/serial-getty@ttyS0.service`
	nativeHelperFamilyProc(t, root, frame, 43, 1, 42, 1000, 101, group)
	// An unrelated host service must also be readable by the complete census.
	nativeHelperFamilyProc(t, root, frame, 44, 1, 999, 0, 102, group)
	tasks, err := (nativeProcessProbe{root: root}).helperTasks(t.Context(), []nativeHostHelperRecord{frame})
	if err != nil || len(tasks) != 1 || tasks[0].ObservedCgroup != group || tasks[0].Relation != "original_session_candidate" {
		t.Fatal("literal cgroup path changed or hid family", tasks, err)
	}
	if _, err := nativeHostHelperParent("0::/" + group + "\n"); err == nil {
		t.Fatal("observation relaxed effect authority")
	}
}

func TestNativeHelperCgroupObservationRejectsAmbiguousPaths(t *testing.T) {
	for _, raw := range []string{"0::/../outside\n", "0::/scope/./child\n", "0::/scope//child\n", "0:://scope\n", "0::/scope\r\n", "0::/scope\n1:name=systemd:/scope\n", "1:name=systemd:/scope\n"} {
		if _, err := nativeObservedHelperCgroup(raw); err == nil {
			t.Fatal("ambiguous observation accepted", raw)
		}
	}
}

func TestNativeHelperFamilyRefusesChangingOrUnreadableEvidence(t *testing.T) {
	for _, scenario := range []string{"parent", "session", "process-group", "start", "cgroup", "uid", "missing-stat", "permission", "duplicate-owner"} {
		t.Run(scenario, func(t *testing.T) {
			_, owner, _ := nativeHelperJournalFixture(t)
			frame := nativeHelperTaskRecord(owner, 42, 100)
			frame.SessionID = 42
			root := t.TempDir()
			dir := nativeHelperFamilyProc(t, root, frame, 43, 1, 42, 1000, 101, "escaped")
			calls := make(map[string]int)
			probe := nativeProcessProbe{root: root, readFile: func(path string) ([]byte, error) {
				calls[path]++
				if path == filepath.Join(dir, "stat") && calls[path] == 3 {
					switch scenario {
					case "parent", "session", "process-group", "start":
						raw, _ := os.ReadFile(path)
						end := strings.LastIndexByte(string(raw), ')')
						fields := strings.Fields(string(raw[end+1:]))
						index := map[string]int{"parent": 1, "session": 3, "process-group": 2, "start": 19}[scenario]
						fields[index] = "999"
						if err := os.WriteFile(path, []byte(string(raw[:end+1])+" "+strings.Join(fields, " ")), 0o600); err != nil {
							t.Fatal(err)
						}
					case "cgroup":
						if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/changed\n"), 0o600); err != nil {
							t.Fatal(err)
						}
					case "uid":
						if err := os.WriteFile(filepath.Join(dir, "status"), []byte("Uid: 0 0 0 0\n"), 0o600); err != nil {
							t.Fatal(err)
						}
					case "missing-stat":
						return nil, os.ErrNotExist
					case "permission":
						return nil, os.ErrPermission
					}
				}
				return os.ReadFile(path)
			}}
			frames := []nativeHostHelperRecord{frame}
			if scenario == "duplicate-owner" {
				other := nativeHelperTaskRecord(owner, 44, 100)
				other.SessionID = 42 // Observation must reject overlapping provenance even before persistence validation.
				frames = append(frames, other)
			}
			if tasks, err := probe.helperTasks(t.Context(), frames); err == nil || tasks != nil {
				t.Fatal("uncertain family accepted", tasks, err)
			}
		})
	}
}

func TestNativeHelperRetirementAndResourceAckRetainOrphanFamily(t *testing.T) {
	j, owner, _ := nativeHelperJournalFixture(t)
	frame := nativeHelperTaskRecord(owner, 42, 100)
	frame.SessionID = 42
	writeNativeHelperTaskFrame(t, j, owner, frame)
	root := t.TempDir()
	j.owner.helperProbe = &nativeProcessProbe{root: root}
	dir := nativeHelperFamilyProc(t, root, frame, 43, 1, 42, 1000, 101, "escaped")
	if err := j.retire(t.Context(), owner, frame.Launch.Generation); err == nil {
		t.Fatal("empty group acknowledged surviving family")
	}
	frames, err := j.records(owner)
	if err != nil || len(frames) != 1 || !frames[0].Launch.Revoked || frames[0].Launch.ExitConfirmed || frames[0].Launch.ResourcesRemoved {
		t.Fatal("uncertain retirement lost original frame", frames, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := j.retire(t.Context(), owner, frame.Launch.Generation); err != nil {
		t.Fatal("absent family did not allow original retry", err)
	}
	// Historical false acknowledgements also cannot release the parent VM.
	nativeHelperFamilyProc(t, root, frame, 43, 1, 42, 1000, 101, "escaped")
	owner.Revoked, owner.ExitConfirmed = true, true
	if err := j.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err == nil {
		t.Fatal("resource publication ignored surviving session")
	}
}

type nativeHelperSessionFixture struct {
	*nativeHelperGroupsFixture
	err error
}

func (g *nativeHelperSessionFixture) OriginalSession(pid int, _ uint64) (int, error) {
	return pid, g.err
}

func TestNativeHelperSessionProvenancePrecedesExecutionGate(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			j, owner, groups := nativeHelperJournalFixture(t)
			origin := &nativeHelperSessionFixture{nativeHelperGroupsFixture: groups}
			if fail {
				origin.err = errors.New("session origin unavailable")
			}
			j.groups = origin
			cmd, marker := nativeHelperFixtureCommand(t)
			published := false
			j.writeValue = func(path string, record nativeHostHelperRecord) error {
				if record.Launch.Authorized && !record.Launch.Revoked {
					if record.SessionID != cmd.Process.Pid || record.SessionPolicy != 1 {
						t.Fatal("private session missing from original publication")
					}
					if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("command ran before session publication")
					}
					published = true
				}
				return writeNativeJournalValue(path, record)
			}
			err := j.run(t.Context(), owner, cmd, time.Second)
			if (err != nil) != fail || published == fail {
				t.Fatal("session failure bypassed gate", published, err)
			}
			if fail {
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unproven session ran command")
				}
			}
		})
	}
}

func TestNativeHelperSessionFramePreservesHistoricalShapeAndRejectsDamage(t *testing.T) {
	_, owner, _ := nativeHelperJournalFixture(t)
	frame := nativeHelperTaskRecord(owner, 42, 100)
	frame.SessionID, frame.SessionPolicy = 0, 0
	old, err := json.Marshal(frame)
	if err != nil || strings.Contains(string(old), "session_id") {
		t.Fatal("historical shape changed", err)
	}
	frame.SessionID = 42
	raw, _ := json.Marshal(frame)
	var got nativeHostHelperRecord
	if json.Unmarshal(raw, &got) != nil || got.validate(owner) != nil || got.SessionID != 42 {
		t.Fatal("original session lost")
	}
	for _, bad := range []string{"null", "0", "43", "-1", "42.0"} {
		data := []byte(strings.Replace(string(raw), `"session_id":42`, `"session_id":`+bad, 1))
		err := json.Unmarshal(data, &got)
		if err == nil && got.validate(owner) == nil {
			t.Fatal("damaged session accepted", bad)
		}
	}
}

func TestNativeHelperFamilyInventoryRechecksAroundConsumerAndTransport(t *testing.T) {
	for _, scenario := range []string{"stable", "parent", "session", "new-child", "exit", "frame", "unrelated"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, _, _, _ := nativeRuntimeFixture(t)
			r := v.nativeRecovery
			owner, err := r.journal.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			j := &nativeHostHelperJournal{owner: r.journal, groups: newNativeHelperGroupsFixture()}
			r.helperGroups = j.groups
			frame := nativeHelperTaskRecord(owner, 43, 102)
			frame.SessionID = 43
			writeNativeHelperTaskFrame(t, j, owner, frame)
			dir := nativeHelperFamilyProc(t, r.retirer.probe.root, frame, 44, 1, 43, 1000, 103, "escaped")
			called := false
			err = v.withNativeRuntimeInventory(t.Context(), func(_ context.Context, sample NativeRuntimeInventory) error {
				called = true
				if len(sample.HelperTasks) != 1 || sample.HelperTasks[0].PID != 44 || len(sample.Unresolved) != 1 || sample.Owners[0].Unresolved == "" {
					t.Fatal("orphan session hidden from inventory", sample)
				}
				wire := NativeRuntimeInventorySample{Version: 1, Host: runtimequota.HostEpoch{NodeID: uuid.NewString(), HostEpochID: uuid.NewString()}, Challenge: uuid.NewString(), Inventory: sample}
				raw, err := EncodeNativeRuntimeInventorySample(wire)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := DecodeNativeRuntimeInventorySample(raw)
				if err != nil || !reflect.DeepEqual(decoded, wire) {
					t.Fatal("family provenance changed on wire", err)
				}
				switch scenario {
				case "parent":
					nativeHelperFamilyProc(t, r.retirer.probe.root, frame, 44, 2, 43, 1000, 103, "escaped")
				case "session":
					nativeHelperFamilyProc(t, r.retirer.probe.root, frame, 44, 1, 900, 1000, 103, "escaped")
				case "new-child":
					nativeHelperFamilyProc(t, r.retirer.probe.root, frame, 45, 44, 900, 1000, 104, "other")
				case "exit":
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
				case "frame":
					frame.SessionID, frame.SessionPolicy = 0, 0
					writeNativeHelperTaskFrame(t, j, owner, frame)
				case "unrelated":
					nativeHelperFamilyProc(t, r.retirer.probe.root, frame, 45, 1, 900, 1000, 104, "other")
				}
				return nil
			})
			if !called || (err == nil) != (scenario == "stable" || scenario == "unrelated") {
				t.Fatal("family changes escaped consumer recheck", called, err)
			}
		})
	}
}

func TestNativeHelperHistoricalSessionMissingRemainsUnresolvedAfterLeaderExit(t *testing.T) {
	v, lease, _, _, _ := nativeRuntimeFixture(t)
	r := v.nativeRecovery
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	j := &nativeHostHelperJournal{owner: r.journal, groups: newNativeHelperGroupsFixture()}
	r.helperGroups = j.groups
	frame := nativeHelperTaskRecord(owner, 43, 102)
	frame.SessionID, frame.SessionPolicy = 0, 0
	frame.Launch.Revoked, frame.Launch.ExitConfirmed, frame.Launch.ResourcesRemoved = true, true, true
	writeNativeHelperTaskFrame(t, j, owner, frame)
	if err := j.retire(t.Context(), owner, frame.Launch.Generation); err == nil {
		t.Fatal("historical acknowledgement supplied missing session")
	}
	if err := j.requireRemoved(t.Context(), owner); err == nil {
		t.Fatal("historical acknowledgement authorized new producer")
	}
	called := false
	err = v.withNativeRuntimeInventory(t.Context(), func(_ context.Context, sample NativeRuntimeInventory) error {
		called = true
		if len(sample.HelperTasks) != 0 || len(sample.Unresolved) != 1 || sample.Owners[0].Unresolved == "" {
			t.Fatal("historical family absence invented", sample)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal("historical provenance became unreadable", err)
	}
}

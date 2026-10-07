package fcvm

// adr: 654
import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type nativeHelperProcess struct {
	PID, ParentPID, ProcessGroupID, SessionID int
	StartTime                                 uint64
	Cgroup                                    string
}

func nativeHelperProcessStat(raw string, pid int) (nativeHelperProcess, error) {
	start, err := nativeProcessStartTime(raw, pid)
	if err != nil {
		return nativeHelperProcess{}, err
	}
	fields := strings.Fields(raw[strings.LastIndexByte(raw, ')')+1:])
	process := nativeHelperProcess{PID: pid, StartTime: start}
	for i, target := range []*int{&process.ParentPID, &process.ProcessGroupID, &process.SessionID} {
		value, err := strconv.ParseUint(fields[i+1], 10, 31)
		if err != nil || strconv.FormatUint(value, 10) != fields[i+1] {
			return nativeHelperProcess{}, errors.New("native inventory: invalid helper family identity")
		}
		*target = int(value)
	}
	if process.ParentPID == pid {
		return nativeHelperProcess{}, errors.New("native inventory: self-parented helper task")
	}
	return process, nil
}

// Read all task relationships, independently of argv/UID filters. Only stable
// related candidates become observations; none can authorize signals or reuse.
func (p nativeProcessProbe) helperProcessCensus(ctx context.Context) (map[int]nativeHelperProcess, error) {
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return nil, err
	}
	result := make(map[int]nativeHelperProcess)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		if pid > math.MaxInt32 || strconv.Itoa(pid) != entry.Name() {
			return nil, errors.New("native inventory: noncanonical helper process directory")
		}
		process, err := p.readHelperProcess(pid)
		if errors.Is(err, os.ErrNotExist) {
			if _, gone := os.Stat(filepath.Join(p.root, entry.Name())); errors.Is(gone, os.ErrNotExist) {
				continue // An unrelated natural exit grants no absence acknowledgement.
			}
		}
		if err != nil {
			return nil, err
		}
		result[pid] = process
	}
	return result, ctx.Err()
}

func (p nativeProcessProbe) readHelperProcess(pid int) (nativeHelperProcess, error) {
	read := p.readFile
	if read == nil {
		read = os.ReadFile
	}
	dir := filepath.Join(p.root, strconv.Itoa(pid))
	var before nativeHelperProcess
	for i := range 2 {
		raw, err := read(filepath.Join(dir, "stat"))
		if err != nil {
			return before, err
		}
		process, err := nativeHelperProcessStat(string(raw), pid)
		if err != nil {
			return before, err
		}
		raw, err = read(filepath.Join(dir, "cgroup"))
		if err != nil {
			return before, err
		}
		process.Cgroup, err = nativeObservedHelperCgroup(string(raw))
		if err != nil {
			return before, err
		}
		if i == 1 && process != before {
			return before, errors.New("native inventory: helper family relationship changed")
		}
		before = process
	}
	return before, nil
}

// Kernel observations may contain literal systemd escape characters. Retain
// their exact path; never decode it or use this parser to authorize host effects.
func nativeObservedHelperCgroup(raw string) (string, error) {
	line := strings.TrimSuffix(raw, "\n")
	path, ok := strings.CutPrefix(line, "0::/")
	if !ok || filepath.IsAbs(path) || strings.ContainsAny(path, "\n\r") || path != "" && filepath.Clean(path) != path {
		return "", errors.New("native inventory: ambiguous helper cgroup observation")
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." || part == "" && path != "" {
			return "", errors.New("native inventory: invalid helper cgroup observation")
		}
	}
	return path, nil
}

func helperFamilyRelations(record nativeHostHelperRecord, census map[int]nativeHelperProcess) map[int]string {
	relations := make(map[int]string)
	children := make(map[int][]int)
	var pending []int
	for pid, process := range census {
		if parent, ok := census[process.ParentPID]; ok && process.StartTime >= parent.StartTime {
			children[parent.PID] = append(children[parent.PID], pid)
		}
		if process.StartTime < record.Launch.StartTime {
			continue
		}
		relation := ""
		switch {
		case pid == record.Launch.PID && process.StartTime == record.Launch.StartTime:
			relation = "original_leader"
		case record.SessionID != 0 && process.SessionID == record.SessionID:
			relation = "original_session_candidate"
		case process.Cgroup == record.Group.Path || strings.HasPrefix(process.Cgroup, record.Group.Path+"/"):
			relation = "original_cgroup_candidate"
		}
		if relation != "" {
			relations[pid] = relation
			pending = append(pending, pid)
		}
	}
	for len(pending) != 0 {
		pid := pending[0]
		pending = pending[1:]
		for _, child := range children[pid] {
			if relations[child] == "" {
				relations[child] = "observed_descendant"
				pending = append(pending, child)
			}
		}
	}
	return relations
}

func (p nativeProcessProbe) appendHelperDescendants(ctx context.Context, records []nativeHostHelperRecord, leaders []NativeRuntimeHelperTask) ([]NativeRuntimeHelperTask, error) {
	if !slices.ContainsFunc(records, func(r nativeHostHelperRecord) bool { return r.Launch.Authorized || r.Group.Inode != 0 }) {
		return leaders, ctx.Err()
	}
	census, err := p.helperProcessCensus(ctx)
	if err != nil {
		return nil, err
	}
	result := slices.Clone(leaders)
	seen := make(map[int]string)
	for _, leader := range leaders {
		seen[leader.PID] = leader.HelperGeneration
		process, ok := census[leader.PID]
		if !ok || process.StartTime != leader.StartTime || process.Cgroup != leader.ObservedCgroup {
			return nil, errors.New("native inventory: original helper changed before family scan")
		}
	}
	for _, record := range records {
		if !record.Launch.Authorized && record.Group.Inode == 0 {
			continue
		}
		for pid, relation := range helperFamilyRelations(record, census) {
			if generation, ok := seen[pid]; ok {
				if generation != record.Launch.Generation {
					return nil, errors.New("native inventory: duplicate helper family ownership")
				}
				continue
			}
			task, err := p.readHelperFamilyTask(census[pid], record, relation)
			if err != nil {
				return nil, err
			}
			seen[pid] = record.Launch.Generation
			result = append(result, task)
		}
	}
	return result, ctx.Err()
}

func (p nativeProcessProbe) readHelperFamilyTask(process nativeHelperProcess, record nativeHostHelperRecord, relation string) (NativeRuntimeHelperTask, error) {
	read := p.readFile
	if read == nil {
		read = os.ReadFile
	}
	dir := filepath.Join(p.root, strconv.Itoa(process.PID))
	uid, group, err := nativeHelperTaskScope(read, dir)
	if err != nil {
		return NativeRuntimeHelperTask{}, err
	}
	current, err := p.readHelperProcess(process.PID)
	if err != nil || current != process || group != process.Cgroup {
		return NativeRuntimeHelperTask{}, errors.Join(err, errors.New("native inventory: helper family incarnation changed"))
	}
	afterUID, afterGroup, err := nativeHelperTaskScope(read, dir)
	if err != nil || uid != afterUID || group != afterGroup {
		return NativeRuntimeHelperTask{}, errors.Join(err, errors.New("native inventory: helper family scope changed"))
	}
	return NativeRuntimeHelperTask{Instance: record.Launch.Lease.Instance, OwnerGeneration: record.OwnerGeneration, HelperGeneration: record.Launch.Generation,
		PID: process.PID, StartTime: process.StartTime, ObservedUID: uid, ExpectedCgroup: record.Group.Path, ObservedCgroup: group,
		RetirementClaimed: record.Launch.ExitConfirmed || record.Launch.ResourcesRemoved,
		LeaderPID:         record.Launch.PID, LeaderStartTime: record.Launch.StartTime, ParentPID: process.ParentPID,
		ProcessGroupID: process.ProcessGroupID, SessionID: process.SessionID, Relation: relation}, nil
}

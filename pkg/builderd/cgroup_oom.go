package builderd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type builderSliceOOMError struct {
	Delta uint64
}

func (e *builderSliceOOMError) Error() string {
	return fmt.Sprintf("builder parent cgroup reported %d oom_kill event(s)", e.Delta)
}

// readCgroupOOMKills reads the cumulative cgroup-v2 oom_kill counter. Keeping
// the parser outside the metal build makes its failure and delta semantics
// testable on ordinary CI runners.
func readCgroupOOMKills(path string) (uint64, error) {
	// The production caller supplies the fixed cgroup-v2 memory.events path;
	// tests supply a private temporary fixture. Neither path is customer input.
	//nolint:forbidigo // vetted system/fixture path; customer path guard does not apply.
	body, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = body.Close() }()
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != "oom_kill" {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("parse oom_kill: %w", parseErr)
		}
		return value, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("memory.events missing oom_kill")
}

func builderSliceOOMDelta(handle BuildHandle, path string) uint64 {
	if !handle.BuilderSliceOOMCounterValid {
		return 0
	}
	current, err := readCgroupOOMKills(path)
	if err != nil || current <= handle.BuilderSliceOOMKillsAtStart {
		return 0
	}
	return current - handle.BuilderSliceOOMKillsAtStart
}

func builderSliceOOMOutcome(handle BuildHandle, path string) (BuildOutcome, bool) {
	delta := builderSliceOOMDelta(handle, path)
	if delta == 0 {
		return BuildOutcome{}, false
	}
	return BuildOutcome{
		BuildID:              handle.BuildID,
		InstanceID:           handle.Instance,
		ExportDir:            handle.ExportDir,
		OCIImage:             handle.ExportDir + "/build/out/image.tar",
		ExitCode:             137,
		FailureClass:         "FailureOOM",
		FailureCode:          api.CodeBuildOOM,
		BuilderSliceOOMKills: delta,
	}, true
}

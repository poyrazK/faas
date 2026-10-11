package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Keep the existing positional log parser unchanged for legacy invocations.
func splitJobLogTask(args []string) ([]string, string, bool, error) {
	remaining := make([]string, 0, len(args))
	task := ""
	found := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--task" || arg == "-task" || strings.HasPrefix(arg, "--task=") || strings.HasPrefix(arg, "-task=") {
			if found {
				return nil, "", false, fmt.Errorf("--task may be supplied only once")
			}
			found = true
			if strings.Contains(arg, "=") {
				task = arg[strings.IndexByte(arg, '=')+1:]
			} else {
				if i+1 >= len(args) {
					return nil, "", false, fmt.Errorf("--task requires a nonnegative task index")
				}
				//nolint:gosec // G602: the i+1 >= len(args) guard above rejects a missing value.
				task = args[i+1]
				i++
			}
			n, err := strconv.Atoi(task)
			if err != nil || n < 0 {
				return nil, "", false, fmt.Errorf("--task requires a nonnegative task index")
			}
			task = strconv.Itoa(n)
			continue
		}
		remaining = append(remaining, arg)
	}
	return remaining, task, found, nil
}

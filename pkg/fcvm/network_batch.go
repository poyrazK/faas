package fcvm

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// runIPSetupCommands batches adjacent ip commands in the same namespace.
// Preserve ordering across namespace switches and non-ip commands. ip's batch
// mode stops at the first error (no -force); callers retain normal rollback.
func (m *Manager) runIPSetupCommands(ctx context.Context, cmds [][]string) error {
	runner, ok := m.run.(InputRunner)
	if !ok {
		return m.runCommands(ctx, cmds)
	}
	for i := 0; i < len(cmds); {
		prefix, line := ipSetupBatchLine(cmds[i])
		end := i + 1
		var script strings.Builder
		if prefix != nil {
			script.WriteString(line)
			for end < len(cmds) {
				nextPrefix, nextLine := ipSetupBatchLine(cmds[end])
				if !slices.Equal(prefix, nextPrefix) {
					break
				}
				script.WriteString(nextLine)
				end++
			}
		}
		if end == i+1 {
			if err := m.runCommands(ctx, cmds[i:end]); err != nil {
				return err
			}
		} else {
			argv := append(slices.Clone(prefix), "-batch", "-")
			if err := runner.RunInput(ctx, argv, []byte(script.String())); err != nil {
				return fmt.Errorf("network setup batch %v: %w", argv, err)
			}
		}
		i = end
	}
	return nil
}

// Only serialize plain tokens accepted unchanged by ip's batch parser.
// Unusual names keep the original argv path, so whitespace, comments, quotes,
// or escapes cannot turn a single argument into additional batch commands.
func ipSetupBatchLine(argv []string) ([]string, string) {
	if len(argv) < 2 || argv[0] != "ip" {
		return nil, ""
	}
	prefixLen := 1
	if len(argv) >= 3 && argv[1] == "netns" && argv[2] == "exec" {
		if len(argv) < 6 || argv[4] != "ip" {
			return nil, ""
		}
		prefixLen = 5
	}
	for _, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, "\\\"'#") || strings.ContainsFunc(arg, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}) {
			return nil, ""
		}
	}
	return argv[:prefixLen], strings.Join(argv[prefixLen:], " ") + "\n"
}

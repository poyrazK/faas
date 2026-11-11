//go:build metal && linux

package e2e_test

import (
	"bufio"
	"bytes"
	"os"
	"sort"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// removeDbNodeRun removes a database-node e2e run directory once nothing is
// mounted below it. vmmd binds the host kernel, the shared base and each
// volume into a database VM's jail, and a VM ended by killing its cgroup
// leaves those binds behind: removing through them would reach the host's
// own files. A mount that will not come off leaves the directory in place.
func removeDbNodeRun(t *testing.T, run string) {
	for range 5 {
		mounts := dbNodeMountsUnder(run)
		if len(mounts) == 0 {
			_ = os.RemoveAll(run)
			return
		}
		// Children sort after their parents; detach them first.
		for i := len(mounts) - 1; i >= 0; i-- {
			_ = unix.Unmount(mounts[i], unix.MNT_DETACH)
		}
	}
	t.Errorf("left %s in place: still mounted below it: %s", run, strings.Join(dbNodeMountsUnder(run), ", "))
}

// dbNodeMountsUnder lists the mount points below dir in lexical order.
func dbNodeMountsUnder(dir string) []string {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 4 && strings.HasPrefix(unescapeMountPath(fields[4]), dir+"/") {
			out = append(out, unescapeMountPath(fields[4]))
		}
	}
	sort.Strings(out)
	return out
}

// unescapeMountPath undoes mountinfo's octal escapes (\040 for a space).
func unescapeMountPath(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

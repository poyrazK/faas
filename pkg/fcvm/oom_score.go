package fcvm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// writeOOMScoreAdj sets a process's oom_score_adj (-1000..1000) under
// procRoot. Borrowed builders get api.BorrowedBuilderOOMScoreAdj so the
// kernel's last resort kills a borrowed build before any tenant VM or daemon
// (ADR-20261118154632).
func writeOOMScoreAdj(procRoot string, pid, score int) error {
	if pid <= 0 {
		return fmt.Errorf("fcvm: oom_score_adj: pid %d is not a process", pid)
	}
	if score < -1000 || score > 1000 {
		return fmt.Errorf("fcvm: oom_score_adj: %d is outside -1000..1000", score)
	}
	path := filepath.Join(procRoot, strconv.Itoa(pid), "oom_score_adj")
	if err := os.WriteFile(path, []byte(strconv.Itoa(score)+"\n"), 0o644); err != nil {
		return fmt.Errorf("fcvm: write %s: %w", path, err)
	}
	return nil
}

package fcvm

// adr: 655
import (
	"errors"
	"strings"
)

func nativeHelperSessionStatus(raw string) error {
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != "NoNewPrivs" && key != "Seccomp" {
			continue
		}
		if seen[key] || key == "NoNewPrivs" && strings.TrimSpace(value) != "1" || key == "Seccomp" && strings.TrimSpace(value) != "2" {
			return errors.New("native helper: session restriction kernel state is unavailable")
		}
		seen[key] = true
	}
	if !seen["NoNewPrivs"] || !seen["Seccomp"] {
		return errors.New("native helper: session restriction kernel state is missing")
	}
	return nil
}

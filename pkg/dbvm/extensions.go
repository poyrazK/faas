package dbvm

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

var extensionFilename = regexp.MustCompile(`^[a-zA-Z0-9_+.\-]{1,199}\.(control|sql)$`)

// BaseExtensionFiles reads the inventory of the exact image configured for
// the target major. debugfs opens it read-only without mounting or booting it.
func (r *Runtime) BaseExtensionFiles(ctx context.Context, major int) ([]string, error) {
	path, ok := r.base(major)
	if !ok {
		return nil, errors.New("target PostgreSQL base is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, dirs := range []struct{ extensions, libraries string }{
		{"/usr/local/share/postgresql/extension", "/usr/local/lib/postgresql"},
		{fmt.Sprintf("/usr/share/postgresql/%d/extension", major), fmt.Sprintf("/usr/lib/postgresql/%d/lib", major)},
	} {
		output, err := exec.CommandContext(ctx, "debugfs", "-R", "ls -p "+dirs.extensions, path).Output()
		if err != nil {
			return nil, errors.New("could not read PostgreSQL base extension inventory")
		}
		if files := extensionDirectory(string(output)); len(files) > 0 {
			if slices.Contains(files, "vector.control") {
				libraries, err := exec.CommandContext(ctx, "debugfs", "-R", "ls -p "+dirs.libraries, path).Output()
				if err != nil {
					return nil, errors.New("could not read PostgreSQL base library inventory")
				}
				if slices.Contains(extensionDirectory(string(libraries)), "vector.so") {
					files = append(files, "vector.so")
				}
			}
			return files, nil
		}
	}
	return nil, errors.New("PostgreSQL base contains no readable extension directory")
}

func extensionDirectory(output string) []string {
	var files []string
	for line := range strings.SplitSeq(output, "\n") {
		parts := strings.Split(line, "/")
		if len(parts) == 8 && strings.HasPrefix(parts[2], "10") &&
			(extensionFilename.MatchString(parts[5]) || parts[5] == "vector.so") {
			files = append(files, parts[5])
		}
	}
	return files
}

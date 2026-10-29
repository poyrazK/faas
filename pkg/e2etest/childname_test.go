package e2etest

import (
	"os/exec"
	"testing"
)

// A daemon started under setpriv is still found by its own name, so the
// Kill helpers reach imaged, which always runs with a reduced bounding set.
func TestChildNameSeesThroughSetpriv(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"/bin/schedd"}, "schedd"},
		{[]string{"/usr/bin/setpriv", "--bounding-set=-all,+chown", "--", "/bin/imaged"}, "imaged"},
		{[]string{"/usr/bin/sudo", "-n", "-E", "/usr/bin/setpriv", "--bounding-set=-all", "--reuid", "1000", "--",
			"env", "PATH=/usr/bin", "/bin/imaged"}, "imaged"},
	} {
		cmd := exec.Command(tc.args[0], tc.args[1:]...)
		cmd.Path = tc.args[0]
		if got := childName(cmd); got != tc.want {
			t.Errorf("childName(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

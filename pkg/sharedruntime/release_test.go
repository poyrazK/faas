package sharedruntime

import "testing"

func TestReleaseNeedsPackageManager(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime string
		command []string
		shell   bool
		want    bool
	}{
		{"no release", "node24", nil, false, false},
		{"npm shell", "node24", []string{"npm run migrate"}, true, true},
		{"npm later in script", "node24", []string{"cd db && npm run migrate"}, true, true},
		{"npx in subshell", "node24", []string{"(npx prisma migrate deploy)"}, true, true},
		{"node script", "node24", []string{"node tasks-migrate.js apply"}, true, false},
		{"local bin", "node24", []string{"prisma migrate deploy"}, true, false},
		{"npm argv", "node24", []string{"/usr/local/bin/npm", "run", "migrate"}, false, true},
		{"node argv", "node24", []string{"node", "migrate.js"}, false, false},
		{"sh -c argv", "node24", []string{"sh", "-c", "npm run migrate"}, false, true},
		{"env wrapper argv", "node24", []string{"/usr/bin/env", "NODE_ENV=production", "npx", "prisma", "migrate", "deploy"}, false, true},
		{"word containing npm", "node24", []string{"./npm-migrate.sh"}, true, false},
		{"python uv", "python313", []string{"uv run alembic upgrade head"}, true, true},
		{"python direct", "python313", []string{"python -m alembic upgrade head"}, true, false},
		{"npm on python runtime", "python313", []string{"npm run x"}, true, false},
	} {
		if got := ReleaseNeedsPackageManager(tc.runtime, tc.command, tc.shell); got != tc.want {
			t.Errorf("%s: ReleaseNeedsPackageManager(%q, %q, %v) = %v, want %v", tc.name, tc.runtime, tc.command, tc.shell, got, tc.want)
		}
	}
}

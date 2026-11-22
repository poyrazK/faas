package sharedruntime

import "testing"

func TestCommandUsesPackageManager(t *testing.T) {
	for _, tc := range []struct {
		command []string
		shell   bool
		want    bool
	}{
		{nil, true, false},
		{[]string{"npm run tasks:migrate -- apply"}, true, true},
		{[]string{"node tasks-migrate.js apply && npx prisma migrate deploy"}, true, true},
		{[]string{"cd /app; (yarn migrate)"}, true, true},
		{[]string{"uv run alembic upgrade head"}, true, true},
		{[]string{"node migrate.js"}, true, false},
		{[]string{"prisma migrate deploy"}, true, false},
		{[]string{"python manage.py migrate --noinput"}, true, false},
		{[]string{"echo npmrc-free"}, true, false},
		{[]string{"/usr/local/bin/npm", "run", "migrate"}, false, true},
		{[]string{"pnpm", "db:migrate"}, false, true},
		{[]string{"/bin/sh", "-c", "npm run migrate"}, false, true},
		{[]string{"node", "migrate.js", "npm"}, false, false},
		{[]string{"alembic", "upgrade", "head"}, false, false},
	} {
		if got := CommandUsesPackageManager(tc.command, tc.shell); got != tc.want {
			t.Errorf("CommandUsesPackageManager(%q, %v) = %v, want %v", tc.command, tc.shell, got, tc.want)
		}
	}
}

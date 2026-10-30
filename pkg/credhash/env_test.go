package credhash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/credhash"
)

func ringFile(t *testing.T, kids ...int) string {
	t.Helper()
	var lines []string
	for _, kid := range kids {
		line, err := credhash.NewKeyLine(kid)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	path := filepath.Join(t.TempDir(), "credential-keys")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// adr: 20261029202403 — the ring and the rollout switches come from unit
// configuration, never the database, and a policy that cannot hold is
// refused at start.
func TestLoadAccountPolicyReadsUnitConfiguration(t *testing.T) {
	path := ringFile(t, 2, 1)
	cases := []struct {
		name    string
		env     map[string]string
		keyed   bool
		plain   bool
		current byte
		wantErr string
	}{
		{name: "nothing configured is the release before", plain: true},
		{name: "expand", env: map[string]string{credhash.EnvAccountKeysFile: path}, plain: true, current: 2},
		{name: "keyed writes", env: map[string]string{credhash.EnvAccountKeysFile: path, credhash.EnvAccountWrites: "keyed"}, keyed: true, plain: true, current: 2},
		{name: "contract", env: map[string]string{credhash.EnvAccountKeysFile: path, credhash.EnvAccountWrites: "keyed", credhash.EnvAccountPlainLookup: "false"}, keyed: true, current: 2},
		{name: "keyed writes need a ring", env: map[string]string{credhash.EnvAccountWrites: "keyed"}, wantErr: "key ring"},
		{name: "unknown write mode", env: map[string]string{credhash.EnvAccountWrites: "hmac"}, wantErr: "plain, keyed"},
		{name: "bad plain lookup", env: map[string]string{credhash.EnvAccountPlainLookup: "maybe"}, wantErr: "boolean"},
		{name: "missing ring file", env: map[string]string{credhash.EnvAccountKeysFile: filepath.Join(t.TempDir(), "absent")}, wantErr: credhash.EnvAccountKeysFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := credhash.LoadAccountPolicy(func(k string) string { return tc.env[k] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.KeyedWrites != tc.keyed || p.PlainLookup != tc.plain {
				t.Fatalf("policy = keyed %v plain %v", p.KeyedWrites, p.PlainLookup)
			}
			if tc.current != 0 && (p.Ring == nil || p.Ring.Current() != tc.current) {
				t.Fatalf("ring = %+v, want current key %d", p.Ring, tc.current)
			}
		})
	}
}

func TestNextKeyIDRotatesPastEveryKey(t *testing.T) {
	cases := []struct {
		kids []int
		want int
	}{
		{kids: []int{1}, want: 2},
		{kids: []int{3, 7, 2}, want: 8},
		{kids: []int{255, 1, 2}, want: 3},
	}
	for _, tc := range cases {
		got, err := ring(t, tc.kids...).NextKeyID()
		if err != nil || got != tc.want {
			t.Fatalf("NextKeyID(%v) = %d, %v; want %d", tc.kids, got, err, tc.want)
		}
	}
}

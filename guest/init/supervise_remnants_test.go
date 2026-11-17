package main

import (
	"io/fs"
	"syscall"
	"testing"
	"time"
)

// fakeLeaf models one cgroup leaf: a process leaves when it gets a signal it
// does not ignore.
type fakeLeaf struct {
	procs   map[int]bool // pid -> ignores SIGTERM
	signals []string
	clock   time.Time
	missing bool
}

func (f *fakeLeaf) reaper() remnantReaper {
	return remnantReaper{
		readProcs: func(string) ([]byte, error) {
			if f.missing {
				return nil, fs.ErrNotExist
			}
			out := ""
			for pid := range f.procs {
				out += itoa(pid) + "\n"
			}
			return []byte(out), nil
		},
		kill: func(pid int, sig syscall.Signal) error {
			f.signals = append(f.signals, itoa(pid)+":"+sig.String())
			if sig == syscall.SIGKILL || !f.procs[pid] {
				delete(f.procs, pid)
			}
			return nil
		},
		now:   func() time.Time { return f.clock },
		sleep: func(d time.Duration) { f.clock = f.clock.Add(d) },
		self:  1,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func TestRemnantReaperEmptiesTheWorkloadLeaf(t *testing.T) {
	for _, tc := range []struct {
		name      string
		leaf      string
		procs     map[int]bool
		missing   bool
		signalled int
		wantErr   bool
		want      []string
	}{
		{name: "orphaned server gets SIGTERM", leaf: "/cg/main-app", procs: map[int]bool{42: false}, signalled: 1, want: []string{"42:terminated"}},
		{name: "SIGTERM ignored escalates to SIGKILL", leaf: "/cg/main-app", procs: map[int]bool{42: true}, signalled: 1, want: []string{"42:terminated", "42:killed"}},
		{name: "empty leaf", leaf: "/cg/main-app", procs: map[int]bool{}},
		{name: "leaf gone", leaf: "/cg/main-app", missing: true},
		{name: "never the cgroup root", leaf: "/cg", procs: map[int]bool{42: false}},
		{name: "never a nested or foreign path", leaf: "/cg/main-app/child", procs: map[int]bool{42: false}},
		{name: "no leaf recorded", leaf: "", procs: map[int]bool{42: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeLeaf{procs: tc.procs, missing: tc.missing, clock: time.Unix(0, 0)}
			n, err := f.reaper().reap("/cg", tc.leaf)
			if (err != nil) != tc.wantErr || n != tc.signalled {
				t.Fatalf("reap = %d, %v; want %d, err=%v", n, err, tc.signalled, tc.wantErr)
			}
			if len(f.signals) != len(tc.want) {
				t.Fatalf("signals = %v, want %v", f.signals, tc.want)
			}
			for i := range tc.want {
				if f.signals[i] != tc.want[i] {
					t.Fatalf("signals = %v, want %v", f.signals, tc.want)
				}
			}
		})
	}
}

func TestRemnantReaperGivesUpOnAnUnkillableProcess(t *testing.T) {
	f := &fakeLeaf{procs: map[int]bool{42: true}, clock: time.Unix(0, 0)}
	r := f.reaper()
	r.kill = func(int, syscall.Signal) error { return nil } // stuck in D state
	if _, err := r.reap("/cg", "/cg/main-app"); err == nil {
		t.Fatal("reap returned nil while a process stayed in the leaf")
	}
	if waited := f.clock.Sub(time.Unix(0, 0)); waited < remnantTermGrace+remnantKillWait || waited > remnantTermGrace+remnantKillWait+time.Second {
		t.Fatalf("waited %s, want about %s", waited, remnantTermGrace+remnantKillWait)
	}
}

func TestParseCgroupProcsSkipsInitAndSelf(t *testing.T) {
	got := parseCgroupProcs([]byte("1\n7\n42\nbad\n\n"), 7)
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("pids = %v, want [42]", got)
	}
}

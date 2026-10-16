package liveproto

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type session struct {
	Name  string `json:"name"`
	Steps []struct {
		Frame  json.RawMessage `json:"frame"`
		Error  bool            `json:"error"`
		Expect *struct {
			Ready   bool   `json:"ready"`
			Version uint64 `json:"version"`
			Rows    []Row  `json:"rows"`
			Stale   bool   `json:"stale"`
			Closed  bool   `json:"closed"`
			Expires int64  `json:"expires"`
		} `json:"expect"`
		Sees map[string]bool `json:"sees"`
	} `json:"steps"`
}

// adr: 1384 — the reference client applies the shared session fixtures
// exactly; the JavaScript SDK runs the same file.
func TestSessions(t *testing.T) {
	data, err := os.ReadFile("testdata/sessions.json")
	if err != nil {
		t.Fatal(err)
	}
	var sessions []session
	if err := json.Unmarshal(data, &sessions); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		t.Run(s.Name, func(t *testing.T) {
			var r Result
			for i, step := range s.Steps {
				frame, err := ParseServerFrame(step.Frame)
				if err == nil {
					err = r.Apply(frame)
				}
				if step.Error {
					if !errors.Is(err, ErrProtocol) {
						t.Fatalf("step %d: err = %v, want a protocol error", i, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if e := step.Expect; e != nil {
					if r.Ready() != e.Ready || r.Stale != e.Stale || r.Closed != e.Closed {
						t.Fatalf("step %d: ready=%v stale=%v closed=%v", i, r.Ready(), r.Stale, r.Closed)
					}
					if e.Ready && r.Version != e.Version {
						t.Fatalf("step %d: version %d, want %d", i, r.Version, e.Version)
					}
					if e.Expires != 0 && r.Expires != e.Expires {
						t.Fatalf("step %d: expires %d, want %d", i, r.Expires, e.Expires)
					}
					if e.Rows != nil && !reflect.DeepEqual(r.Rows(), e.Rows) {
						got, _ := json.Marshal(r.Rows())
						t.Fatalf("step %d: rows %s", i, got)
					}
				}
				for x, want := range step.Sees {
					xid, _ := strconv.ParseUint(x, 10, 64)
					if r.Sees(xid) != want {
						t.Fatalf("step %d: sees(%s) = %v, want %v at %s", i, x, !want, want, r.Snapshot)
					}
				}
			}
		})
	}
}

// adr: 1384 — client frames are validated before realtimed acts on them.
func TestParseClientFrame(t *testing.T) {
	for _, tc := range []struct {
		frame string
		ok    bool
	}{
		{`{"type":"live.subscribe","subscription":"q1","sealed":"gls1.x.y"}`, true},
		{`{"type":"live.subscribe","subscription":"q-1_A","sealed":"gls1.x.y","version":7}`, true},
		{`{"type":"live.renew","subscription":"q1","sealed":"gls1.x.y"}`, true},
		{`{"type":"live.unsubscribe","subscription":"q1"}`, true},
		{`{"type":"live.subscribe","subscription":"q1"}`, false},
		{`{"type":"live.renew","subscription":"q1","sealed":"s","version":3}`, false},
		{`{"type":"live.unsubscribe","subscription":"q1","sealed":"s"}`, false},
		{`{"type":"live.subscribe","subscription":"","sealed":"s"}`, false},
		{`{"type":"live.subscribe","subscription":"a b","sealed":"s"}`, false},
		{`{"type":"live.subscribe","subscription":"` + strings.Repeat("a", api.LiveQuerySubscriptionIDMax+1) + `","sealed":"s"}`, false},
		{`{"type":"live.rows","subscription":"q1"}`, false},
		{`{"type":"live.subscribe","subscription":"q1","sealed":"` + strings.Repeat("a", api.LiveQueryClientFrameMaxBytes) + `"}`, false},
		{`not json`, false},
	} {
		_, err := ParseClientFrame([]byte(tc.frame))
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, ErrProtocol)) {
			t.Errorf("%.80s: err = %v", tc.frame, err)
		}
	}
}

// adr: 1384 — a large result splits into frames under the limit that
// reassemble to the same result.
func TestRowsSplit(t *testing.T) {
	f := Rows{Subscription: "s", Database: "db", Version: 9, Snapshot: "5:5:", Columns: []Column{{Name: "v", Type: "text"}}}
	for i := 0; i < 500; i++ {
		v := strings.Repeat("x", i%97)
		f.Rows = append(f.Rows, Row{ID: strconv.Itoa(i), Values: []*string{&v}})
	}
	frames, err := f.Split(4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 5 {
		t.Fatalf("%d frames", len(frames))
	}
	var r Result
	for i, part := range frames {
		b, err := json.Marshal(part)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > 4096 {
			t.Fatalf("frame %d is %d bytes", i, len(b))
		}
		if part.Complete != (i == len(frames)-1) {
			t.Fatalf("frame %d complete=%v", i, part.Complete)
		}
		frame, err := ParseServerFrame(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Apply(frame); err != nil {
			t.Fatal(err)
		}
	}
	if !r.Ready() || len(r.Rows()) != 500 {
		t.Fatalf("reassembled %d rows", len(r.Rows()))
	}
	empty, err := (Rows{Snapshot: "1:1:"}).Split(4096)
	if err != nil || len(empty) != 1 || !empty[0].Complete || empty[0].Rows == nil {
		t.Fatalf("empty result: %+v %v", empty, err)
	}
	big := strings.Repeat("x", 5000)
	if _, err := (Rows{Rows: []Row{{ID: "a", Values: []*string{&big}}}}).Split(4096); err == nil {
		t.Fatal("oversized row accepted")
	}
}

// adr: 1384 — Covers decides whether a snapshot is not older than another.
func TestSnapshotCovers(t *testing.T) {
	for _, tc := range []struct {
		s, o string
		want bool
	}{
		{"100:110:105", "100:110:105", true},
		{"100:111:", "100:110:105", true},
		{"100:110:105", "100:111:", false},
		{"100:109:", "100:110:109", true},
		{"100:109:", "100:110:", false},
		{"104:108:104", "100:106:", false},
		{"105:108:106", "100:106:", true},
		{"10:10:", "0:0:", true},
	} {
		s, err := ParseSnapshot(tc.s)
		if err != nil {
			t.Fatal(err)
		}
		o, err := ParseSnapshot(tc.o)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Covers(o); got != tc.want {
			t.Errorf("%s covers %s = %v", tc.s, tc.o, got)
		}
	}
	for _, bad := range []string{"", "1:2", "3:2:", "1:5:7", "a:b:"} {
		if _, err := ParseSnapshot(bad); err == nil {
			t.Errorf("ParseSnapshot(%q) accepted", bad)
		}
	}
}

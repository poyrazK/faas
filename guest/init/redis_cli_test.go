package main

import (
	"errors"
	"strings"
	"testing"
)

func TestRedisCLIRequestEncodesBulkStrings(t *testing.T) {
	got := string(redisCLIRequest([]string{"SET", "k", "a b\r\n"}))
	want := "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$5\r\na b\r\n\r\n"
	if got != want {
		t.Fatalf("request = %q, want %q", got, want)
	}
}

// Replies print as redis-cli prints them on a terminal.
func TestRedisCLIRepliesFormatLikeRedisCLI(t *testing.T) {
	for _, tc := range []struct {
		name, wire, want string
	}{
		{"simple", "+PONG\r\n", "PONG\n"},
		{"error", "-ERR unknown command\r\n", "(error) ERR unknown command\n"},
		{"integer", ":42\r\n", "(integer) 42\n"},
		{"bulk", "$5\r\nhello\r\n", "\"hello\"\n"},
		{"bulk with newline", "$3\r\na\nb\r\n", "\"a\\nb\"\n"},
		{"nil bulk", "$-1\r\n", "(nil)\n"},
		{"empty array", "*0\r\n", "(empty array)\n"},
		{"array", "*2\r\n$1\r\na\r\n:1\r\n", "1) \"a\"\n2) (integer) 1\n"},
		{"nested", "*2\r\n*2\r\n$1\r\nx\r\n$1\r\ny\r\n$1\r\nz\r\n", "1) 1) \"x\"\n   2) \"y\"\n2) \"z\"\n"},
		{"wide array aligns labels", "*10\r\n" + strings.Repeat(":1\r\n", 10), " 1) (integer) 1\n" + strings.Repeat("", 0)},
		{"resp3 null", "_\r\n", "(nil)\n"},
		{"resp3 double", ",1.5\r\n", "(double) 1.5\n"},
		{"resp3 bool", "#t\r\n", "(true)\n"},
		{"resp3 map", "%1\r\n+k\r\n:1\r\n", "1) k\n2) (integer) 1\n"},
		{"resp3 verbatim", "=7\r\ntxt:abc\r\n", "abc\n"},
	} {
		reply, err := readRedisCLIReply(strings.NewReader(tc.wire), 1<<20)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := formatRedisCLIReply(reply)
		if tc.name == "wide array aligns labels" {
			if !strings.HasPrefix(got, " 1) (integer) 1\n") || !strings.Contains(got, "10) (integer) 1\n") {
				t.Errorf("%s: %q", tc.name, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRedisCLIReplyIsBounded(t *testing.T) {
	big := "$100\r\n" + strings.Repeat("x", 100) + "\r\n"
	if _, err := readRedisCLIReply(strings.NewReader(big), 64); !errors.Is(err, errRedisCLIReplyTooLarge) {
		t.Fatalf("oversized bulk: err = %v", err)
	}
	if _, err := readRedisCLIReply(strings.NewReader("*-5\r\n"), 64); err == nil {
		t.Fatal("negative array length accepted")
	}
	if _, err := readRedisCLIReply(strings.NewReader(strings.Repeat("*1\r\n", 40)+":1\r\n"), 1<<20); err == nil {
		t.Fatal("deeply nested reply accepted")
	}
	if _, err := readRedisCLIReply(strings.NewReader("$5\r\nab"), 64); err == nil {
		t.Fatal("truncated reply accepted")
	}
}

func TestRedisCLIConnectionHintNamesTheBinding(t *testing.T) {
	got := redisCLIConnectionHint("sessions.svc.gregale:6379", errors.New("connection reset by peer"))
	for _, want := range []string{"sessions.svc.gregale:6379", "gregale redis bind sessions --app APP", "gregale redis get sessions"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint lacks %q: %s", want, got)
		}
	}
}

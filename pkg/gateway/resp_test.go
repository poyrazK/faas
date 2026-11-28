package gateway

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

// A command forwarded through the session must reach the cache byte for byte.
// respReadBulk kept the argument's "$<len>" line as a slice of the reader's
// buffer while it read the payload; a payload read that refilled the buffer
// shifted it and overwrote the line, so the session forwarded garbage in its
// place. On prod a 900-command pipeline was reset part-way (2026-11-28 hunt);
// a misframed length could also make the cache read a value as commands.
// A small buffer fed one byte at a time refills on almost every read.
func TestRESPCommandHeadForwardsExactBytes(t *testing.T) {
	var in bytes.Buffer
	for i := 0; i < 50; i++ {
		in.Write(respEncodeCommand([]byte("SET"), []byte("key:"+strconv.Itoa(i)), []byte(strings.Repeat("v", i*7))))
		in.Write(respEncodeCommand([]byte("AUTH"), []byte("default"), []byte("pw"+strconv.Itoa(i))))
	}
	want := in.Bytes()
	r := bufio.NewReaderSize(iotest.OneByteReader(bytes.NewReader(want)), 16)
	var out bytes.Buffer
	for out.Len() < len(want) {
		cmd, err := respReadCommandHead(r)
		if err != nil {
			t.Fatalf("read command head after %d bytes: %v", out.Len(), err)
		}
		if redisKeptCommands[cmd.name] {
			if err := cmd.readRest(r); err != nil {
				t.Fatalf("readRest: %v", err)
			}
			out.Write(cmd.raw)
			continue
		}
		out.Write(cmd.raw)
		if err := cmd.copyRest(r, &out); err != nil {
			t.Fatalf("copyRest: %v", err)
		}
	}
	if !bytes.Equal(out.Bytes(), want) {
		at := 0
		for at < len(want) && at < out.Len() && want[at] == out.Bytes()[at] {
			at++
		}
		t.Fatalf("forwarded bytes differ at offset %d: got %q, want %q", at, out.Bytes()[at:min(at+20, out.Len())], want[at:min(at+20, len(want))])
	}
}

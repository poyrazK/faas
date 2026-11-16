// Package reseed joins a Go program to Gregale's restore reseed barrier
// (ADR-20261110150000).
//
// Gregale restores parked apps from memory snapshots. Every instance restored
// from one snapshot starts with the random state the program held when the
// snapshot was taken. crypto/rand reads the kernel on every call and is
// always safe. The global generators of math/rand and math/rand/v2 are not,
// and nothing outside the program can reseed them: math/rand/v2's has no
// reseed API, and since Go 1.24 math/rand.Seed does nothing.
//
// Import the package for its side effect to register the process:
//
//	import _ "github.com/gregale-dev/gregale-go/reseed"
//
// and reseed any generator your program owns in an OnRestore callback:
//
//	var rng = rand.New(rand.NewPCG(1, 2))
//
//	func init() {
//		reseed.OnRestore(func(seed [32]byte) error {
//			rng = rand.New(rand.NewChaCha8(seed))
//			return nil
//		})
//	}
//
// Gregale runs every callback after each restore and before the instance
// serves traffic. A callback that returns an error or panics fails the
// restore closed: Gregale cold-boots the instance instead (ADR-005).
// Registered processes are no longer reported as unreached by the barrier.
//
// Outside Gregale, or with GREGALE_RESTORE_RESEED=off, the package does
// nothing.
package reseed

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	socketEnv = "GREGALE_RESEED_SOCKET"
	optOutEnv = "GREGALE_RESTORE_RESEED"
)

var (
	mu        sync.Mutex
	callbacks []func(seed [32]byte) error
)

// OnRestore registers fn to run after every snapshot restore, before the
// instance serves traffic. seed is fresh for each restore and each instance.
// Callbacks run in registration order on one goroutine.
func OnRestore(fn func(seed [32]byte) error) {
	if fn == nil {
		return
	}
	mu.Lock()
	callbacks = append(callbacks, fn)
	mu.Unlock()
}

func init() {
	path := os.Getenv(socketEnv)
	if path == "" || strings.EqualFold(os.Getenv(optOutEnv), "off") {
		return
	}
	go serve(path)
}

func serve(path string) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprintf(conn, "hello go %d\n", os.Getpid()); err != nil {
		return
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		nonce, ok := strings.CutPrefix(strings.TrimSpace(line), "reseed ")
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(conn, "%s\n", reply(strings.TrimSpace(nonce))); err != nil {
			return
		}
	}
}

// reply runs the callbacks and answers the barrier: "ok", or "err <reason>".
func reply(nonceHex string) (answer string) {
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil || len(nonce) < 16 {
		return "err bad_nonce"
	}
	defer func() {
		if r := recover(); r != nil {
			answer = "err panic"
		}
	}()
	mu.Lock()
	fns := append([]func([32]byte) error(nil), callbacks...)
	mu.Unlock()
	for i, fn := range fns {
		var seed [32]byte
		if _, err := rand.Read(seed[:]); err != nil {
			return "err urandom"
		}
		for j := range seed {
			seed[j] ^= nonce[j%len(nonce)]
		}
		if err := fn(seed); err != nil {
			return "err callback_" + strconv.Itoa(i)
		}
	}
	return "ok"
}

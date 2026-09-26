// job-fixture is the static scratch-image workload used by native jobs tests.
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: job-fixture success|fail|sleep|oom")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "success":
		fmt.Println("job fixture succeeded")
	case "fail":
		fmt.Fprintln(os.Stderr, "job fixture failed as requested")
		os.Exit(1)
	case "sleep":
		duration := 5 * time.Minute
		if len(os.Args) > 2 {
			var err error
			duration, err = time.ParseDuration(os.Args[2])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
		}
		time.Sleep(duration)
	case "oom":
		// Retain and touch more than the test VM's 512 MiB allocation.
		// Disabling GC prevents the runtime from reclaiming older chunks.
		debug.SetGCPercent(-1)
		chunks := make([][]byte, 0, 1024)
		for i := 0; i < 1024; i++ {
			chunk := make([]byte, 1<<20)
			for page := 0; page < len(chunk); page += 4096 {
				chunk[page] = byte(i)
			}
			chunks = append(chunks, chunk)
		}
		runtime.KeepAlive(chunks)
	default:
		fmt.Fprintf(os.Stderr, "unknown job fixture mode %q\n", os.Args[1])
		os.Exit(2)
	}
}

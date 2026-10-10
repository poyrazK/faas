// job-fixture is the static scratch-image workload used by native jobs tests.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/jobresult"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: job-fixture success|fail|sleep|oom|contract|customer-operation|customer-workflow")
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
	case "contract":
		runContract()
	case "customer-operation":
		if err := customerOperationFromEnv(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "customer-workflow":
		if err := serveCustomerWorkflow(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown job fixture mode %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runContract() {
	inputID := os.Getenv("GREGALE_INPUT_ID")
	inputRef := os.Getenv("GREGALE_INPUT_REF")
	index := os.Getenv("GREGALE_TASK_INDEX")
	count := os.Getenv("GREGALE_TASK_COUNT")
	manifestPath := os.Getenv("GREGALE_OUTPUT_MANIFEST_PATH")
	if os.Getenv("GREGALE_RUN_ID") == "" || inputID == "" || inputRef == "" ||
		os.Getenv("GREGALE_TASK_ATTEMPT") != "1" ||
		os.Getenv("GREGALE_PARTITION_INDEX") != index ||
		os.Getenv("GREGALE_PARTITION_COUNT") != count ||
		os.Getenv("CONTRACT_MARKER") != "run" || manifestPath == "" {
		fmt.Fprintln(os.Stderr, "missing or inconsistent job execution identity")
		os.Exit(2)
	}
	parsedIndex, indexErr := strconv.Atoi(index)
	parsedCount, countErr := strconv.Atoi(count)
	if indexErr != nil || countErr != nil || parsedIndex < 0 || parsedIndex >= parsedCount {
		fmt.Fprintln(os.Stderr, "invalid task partition identity")
		os.Exit(2)
	}
	if inputID == "fail" {
		fmt.Fprintln(os.Stderr, "declared input failed")
		os.Exit(1)
	}
	result := []byte(inputID + ":" + inputRef)
	manifest := jobresult.Manifest{Version: jobresult.Version, Artifacts: []jobresult.Artifact{{
		Name: "result", URI: "s3://job-contract-results/" + inputID + ".bin",
		SizeBytes: int64(len(result)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(result)),
	}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("processed input %s at task %s/%s\n", inputID, index, count)
}

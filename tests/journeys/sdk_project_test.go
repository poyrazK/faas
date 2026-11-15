package journeys

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSDKReviewedProject exercises the public Node SDK against a deployed API,
// including a hard process exit after acceptance and source-free receipt recovery.
func TestSDKReviewedProject(t *testing.T) {
	tg := needs(t, tierFull, true)
	module := os.Getenv("GREGALE_E2E_NODE_SDK")
	if module == "" || tg.token == "" {
		t.Skip("set GREGALE_E2E_NODE_SDK to a built SDK entry and GREGALE_E2E_TOKEN")
	}
	module, err := filepath.Abs(module)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(module); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node 22+ is required")
	}
	example, err := filepath.Abs(filepath.Join("..", "..", "examples", "sdk-project-application"))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	source, state := filepath.Join(work, "source"), filepath.Join(work, "state")
	if err = os.CopyFS(source, os.DirFS(filepath.Join(example, "source"))); err != nil {
		t.Fatal(err)
	}
	project := tg.slug("sdk")
	apps := []string{project + "-api", project + "-worker"}
	slots := 0
	database := ""
	applied := false
	t.Cleanup(func() {
		defer func() {
			for range slots {
				<-tg.appSlots
			}
		}()
		if tg.keep && t.Failed() {
			return
		}
		if applied {
			tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
			for _, app := range apps {
				tg.run(t, 2*time.Minute, "app", "delete", app, "--yes")
			}
			if database != "" {
				tg.run(t, 5*time.Minute, "postgres", "delete", database, "--purge", "--yes")
			}
		}
	})
	tg.acquireAppSlot(t)
	slots++
	if cap(tg.appSlots) < 2 {
		t.Skip("SDK project needs two free application slots")
	}
	tg.acquireAppSlot(t)
	slots++
	environment := append(tg.cliEnv(tg.token), "FAAS_API="+tg.api, "GREGALE_NODE_SDK="+module, "GREGALE_PROJECT_SOURCE="+source, "GREGALE_PROJECT_STATE="+state, "GREGALE_PROJECT_SLUG="+project)
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 25*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, node, args...)
		cmd.Env = environment
		return cmd.CombinedOutput()
	}
	driver := filepath.Join(example, "deploy.mjs")
	preview, err := run(driver, "preview")
	if err != nil {
		t.Fatalf("SDK preview failed: %s", tg.redact(tail(string(preview), 2000)))
	}
	doc := object(t, preview)
	if field(doc, "canApply") != true {
		t.Skip("target cannot apply this project plan")
	}
	managed, _ := field(doc, "managed").([]any)
	for _, raw := range managed {
		row, _ := raw.(map[string]any)
		if str(row, "kind") != "postgres" {
			continue
		}
		if field(row, "provision.existing") == true {
			t.Skip("journey requires a new managed database")
		}
		database = str(row, "provision.database")
	}
	if database == "" {
		t.Skip("installation cannot provision the example's PostgreSQL dependency")
	}
	// Exit only after the apply response and journal are durable. This is a new
	// process boundary, not an in-memory exception or a repeated POST.
	stop := `import {readFile} from 'node:fs/promises';import {pathToFileURL} from 'node:url';import {resolve} from 'node:path';
const sdk=await import(pathToFileURL(process.env.GREGALE_NODE_SDK).href);const state=process.env.GREGALE_PROJECT_STATE;
const plan=sdk.parseReviewedProjectPlan(JSON.parse(await readFile(resolve(state,'plan.json'),'utf8')));
const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN});
await client.projects.applyPlan(plan,{statePath:resolve(state,'apply.json'),timeoutMs:1200000,onProgress:event=>{if(event.phase==='recovering')process.exit(23);}});
process.exit(24);`
	applied = true
	output, err := run("--input-type=module", "-e", stop)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("expected stop after acceptance: %v %s", err, tg.redact(tail(string(output), 2000)))
	}
	if err = os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	resumed, err := run(driver, "resume")
	if err != nil {
		t.Fatalf("SDK resume failed: %s", tg.redact(tail(string(resumed), 2000)))
	}
	result := object(t, resumed)
	if str(result, "outcome") != "succeeded" {
		t.Fatalf("project did not become ready: %s", compact(result))
	}
	rows, _ := field(result, "workloads").([]any)
	if len(rows) != 2 {
		t.Fatalf("expected API and worker outcomes: %s", compact(result))
	}
	tg.ok(t, time.Minute, "app", apps[0], "--no-require-authn")
	eventually(t, "worker writes visible through the API", 3*time.Minute, 3*time.Second, func() (bool, string) {
		response := get(strings.TrimRight(tg.appURL(apps[0]), "/") + "/")
		if response.status != 200 {
			return false, response.String()
		}
		value := jsonBody(t, response)
		return num(value, "processed") > 0, compact(value)
	})
}

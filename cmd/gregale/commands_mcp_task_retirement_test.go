package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestMCPTaskRetirementFixedReadOnlyGate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	for _, tc := range []struct {
		mode, phase         string
		fenced, stopped, ok bool
		parks               int
	}{
		{"qualify", "qualify", true, false, true, 0}, {"park", "park", true, true, true, 1}, {"pending", "observe", false, true, true, 0}, {"denied", "park", true, true, false, 0}, {"missing_fence", "park", false, true, false, 0}, {"live_registration", "observe", true, false, false, 0}, {"native_live", "observe", true, true, false, 0}, {"compatibility", "park", true, true, false, 0}, {"lost_reply", "park", true, true, false, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			worker := "12345678-1234-1234-1234-123456789abc"
			write := func(name, body string) {
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			state, _ := json.Marshal(map[string]any{"mode": tc.mode, "fenced": tc.fenced, "stopped": tc.stopped, "worker": worker, "parks": 0, "observations": 0})
			write("state.json", string(state))
			write("package.json", `{"type":"module"}`)
			write("gregale-mcp.json", `{}`)
			write("node_modules/pg/package.json", `{"name":"pg","main":"index.cjs"}`)
			write("node_modules/pg/index.cjs", `const fs=require('node:fs');module.exports={Pool:class{async connect(){return this}release(){}async end(){}async query(sql){if(!/^(SELECT|BEGIN READ ONLY|COMMIT|ROLLBACK)/.test(sql))throw Error('WRITE_ATTEMPT');const s=JSON.parse(fs.readFileSync('state.json'));if(sql.startsWith('SELECT worker_id'))return {rows:[{worker_id:s.worker,draining:s.fenced,expired:s.stopped}]};if(sql.startsWith('SELECT clock_timestamp'))return {rows:[{observed_at:new Date()}]};return {rows:[]};}}};`)
			write("task-runtime.js", `export function resolveMcpTaskSettings(c,{role}){if(role!=='observer')throw Error('WRITE_ROLE');return{namespace:'test-namespace',databaseURL:'observer'};}`)
			write("tasks.js", `export const mcpTaskHandlers={};`)
			write("task-release.js", `import{readFileSync}from'node:fs';export async function restoreMcpTasks({restore}){try{if(JSON.parse(readFileSync('state.json')).mode==='compatibility')throw Error('SECRET_SENTINEL');await restore();return{ok:true};}catch{return{ok:false};}}export async function quarantineMcpTaskWorkers(){throw Error('WRITE_ATTEMPT');}`)
			write("hook.cjs", `const fs=require('node:fs');let body='';process.stdin.on('data',d=>body+=d);process.stdin.on('end',()=>{const s=JSON.parse(fs.readFileSync('state.json'));const phase=process.argv.at(-1);if(phase==='park'){if(s.mode==='denied')process.exit(1);s.parks++;fs.writeFileSync('state.json',JSON.stringify(s));if(s.mode==='lost_reply'){console.error('SECRET_SENTINEL');process.exit(1);}console.log('{"parked":true}');}else if(phase==='qualify'){console.log('{"qualified":true}');}else{if(s.mode==='native_live')process.exit(1);const p=JSON.parse(body);if(!p.registrations_stopped)process.exit(1);s.observations++;fs.writeFileSync('state.json',JSON.stringify(s));console.log('{"observed":true}');}});`)
			input, _ := json.Marshal(map[string]any{"command": []string{node, filepath.Join(dir, "hook.cjs")}, "timeoutMs": 1000, "maxBufferBytes": 65536, "phase": tc.phase, "workerIDs": []string{worker}, "workerIDsMax": 1024})
			cmd := exec.Command(node, "--input-type=module", "--eval", main.TaskRetirementGateForTest(), string(input))
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.ok || strings.Contains(string(out), "SECRET_SENTINEL") || strings.Contains(string(out), "WRITE_ATTEMPT") {
				t.Fatalf("gate outcome %v %s", err, out)
			}
			var final struct{ Parks, Observations int }
			b, err := os.ReadFile(filepath.Join(dir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if json.Unmarshal(b, &final) != nil || final.Parks != tc.parks {
				t.Fatal("unexpected repeated parking", string(b))
			}
			if tc.ok && tc.phase != "qualify" && final.Observations != 1 {
				t.Fatal("success without shutdown observation")
			}
		})
	}
}

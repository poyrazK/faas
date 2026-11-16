package main_test

import (
	"encoding/json"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPTaskQuarantineFixedGateStages(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	for _, tc := range []struct {
		mode               string
		mutate, fenced, ok bool
		writes, claims     int
	}{{"success", true, false, true, 1, 1}, {"readonly", false, true, true, 0, 0}, {"lost_sql_reply", true, false, true, 1, 1}, {"denied", true, false, false, 0, 0}, {"unconfirmed", false, false, false, 0, 0}, {"write_refused", true, false, false, 1, 1}, {"foreign_namespace", true, false, false, 0, 1}, {"compatibility", true, false, false, 0, 0}, {"lost_observe_reply", true, false, false, 1, 1}} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			worker := "12345678-1234-1234-1234-123456789abc"
			write := func(name, value string) {
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			state, _ := json.Marshal(map[string]any{"mode": tc.mode, "draining": tc.fenced, "writes": 0, "claims": 0, "observations": 0, "worker": worker})
			write("state.json", string(state))
			write("package.json", `{"type":"module"}`)
			write("node_modules/pg/package.json", `{"name":"pg","main":"index.cjs"}`)
			write("node_modules/pg/index.cjs", `const fs=require('node:fs');module.exports={Pool:class{async connect(){return this;}release(){}async end(){}async query(sql){const s=JSON.parse(fs.readFileSync('state.json'));if(sql.startsWith('SELECT worker_id'))return {rows:s.draining?[{worker_id:s.worker,draining:true}]:[]};if(sql.startsWith('SELECT clock_timestamp'))return {rows:[{observed_at:new Date()}]};return {rows:[]};}}};`)
			write("task-runtime.js", `import{readFileSync}from'node:fs';export function resolveMcpTaskSettings(c,{role}){const s=JSON.parse(readFileSync('state.json'));return {namespace:role==='worker'&&s.mode==='foreign_namespace'?'foreign':'test-namespace',databaseURL:role};}`)
			write("tasks.js", `export const mcpTaskHandlers={};`)
			write("task-release.js", `import{readFileSync,writeFileSync}from'node:fs';export async function restoreMcpTasks({restore}){try{if(JSON.parse(readFileSync('state.json')).mode==='compatibility')throw Error('SECRET_SENTINEL');await restore();return{ok:true};}catch{return{ok:false};}}export async function quarantineMcpTaskWorkers(){const s=JSON.parse(readFileSync('state.json'));s.writes++;if(s.mode!=='write_refused')s.draining=true;writeFileSync('state.json',JSON.stringify(s));if(s.mode==='lost_sql_reply'||s.mode==='write_refused')throw Error('SECRET_SENTINEL');}`)
			write("gregale-mcp.json", `{}`)
			write("hook.cjs", `const fs=require('node:fs');const phase=process.argv.at(-1);const s=JSON.parse(fs.readFileSync('state.json'));if(phase==='claim'){if(s.mode==='denied'){console.error('SECRET_SENTINEL');process.exit(1);}s.claims++;fs.writeFileSync('state.json',JSON.stringify(s));console.log('{"admitted":true}');}else{let body='';process.stdin.on('data',data=>body+=data);process.stdin.on('end',()=>{s.observations++;s.proof=JSON.parse(body);fs.writeFileSync('state.json',JSON.stringify(s));if(s.mode==='lost_observe_reply'){console.error('SECRET_SENTINEL');process.exit(1);}console.log('{"observed":true}');});}`)
			input, _ := json.Marshal(map[string]any{"command": []string{node, filepath.Join(dir, "hook.cjs")}, "timeoutMs": 5000, "maxBufferBytes": 65536, "mutate": tc.mutate, "workerIDs": []string{worker}, "workerIDsMax": 1024})
			cmd := exec.Command(node, "--input-type=module", "--eval", main.TaskQuarantineGateForTest(), string(input))
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.ok || strings.Contains(string(out), "SECRET_SENTINEL") {
				t.Fatalf("gate outcome %v %s", err, out)
			}
			var final struct{ Writes, Claims, Observations int }
			b, err := os.ReadFile(filepath.Join(dir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(b, &final); err != nil {
				t.Fatal(err)
			}
			if final.Writes != tc.writes || final.Claims != tc.claims {
				t.Fatalf("unexpected repeated/admission-free writes %+v", final)
			}
			if tc.ok && final.Observations != 1 {
				t.Fatal("success without recorded fence observation")
			}
		})
	}
}

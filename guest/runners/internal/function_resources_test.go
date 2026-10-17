// adr: 1543
package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

func resourceFixtureEnv(t *testing.T, witness, gate string) func(*ServeConfig) {
	t.Helper()
	return func(cfg *ServeConfig) {
		data, err := templates.FS.ReadFile("rest-api-postgres/runtime/postgres-pool-runtime.js")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(cfg.Spec.Args[0]), "database-runtime.js"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg.Spec.Env = append(cfg.Spec.Env, "POOL_FIXTURE_WITNESS="+witness, "POOL_FIXTURE_DRAIN="+gate)
	}
}

func TestFunctionResourcesRejectOldAdapterBeforeDispatch(t *testing.T) {
	host := &executionWireHost{}
	witness := filepath.Join(t.TempDir(), "handler-called")
	source := strings.Replace(backgroundFunctionSource, "calls++;", "calls++; fs.writeFileSync(process.env.DISPATCH_WITNESS,'called');", 1)
	f := startBackgroundFunction(t, api.PlanFree, 1, false, source, 0, executionWiring(host), func(cfg *ServeConfig) {
		path := cfg.Spec.Args[0]
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		old := strings.Replace(string(data), "resource_lifecycle_version: postgresResources ? 1 : 0", "resource_lifecycle_version: 0", 1)
		if old == string(data) {
			t.Fatal("fixture did not remove the resource capability")
		}
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg.Spec.Env = append(cfg.Spec.Env, "DISPATCH_WITNESS="+witness)
	})
	r := f.request(t, "/state", "old-adapter")
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode < 500 {
		t.Fatal("old adapter admitted qualified execution")
	}
	awaitBackground(t, func() bool { return host.count() == 0 })
	if _, err := os.Stat(witness); !os.IsNotExist(err) {
		t.Fatal("old adapter dispatched customer code")
	}
}

func TestFunctionResourcesRegistrationGateAndCapacity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run("qualified="+strconv.FormatBool(enabled), func(t *testing.T) {
			source := functionResourceFixture + `
export async function handler() {
 let refused=false;
 for(let i=2;i<` + strconv.Itoa(api.FunctionPostgresMaxPools+1) + `;i++) {
   try{databases.push(createPostgresPool(()=>new Pool()))}catch{refused=true;break}
 }
 return {body:JSON.stringify({opened:poolSerial,refused})};
}`
			configure := []func(*ServeConfig){resourceFixtureEnv(t, filepath.Join(t.TempDir(), "pools"), "")}
			if enabled {
				configure = append(configure, executionWiring(&executionWireHost{}))
			} else {
				configure = append(configure, func(cfg *ServeConfig) { cfg.Background = nil })
			}
			f := startBackgroundFunction(t, api.PlanFree, 1, false, source, 0, configure...)
			r := f.request(t, "/state", "capacity")
			defer func() { _ = r.Body.Close() }()
			var result struct {
				Opened  int  `json:"opened"`
				Refused bool `json:"refused"`
			}
			if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			want := api.FunctionPostgresMaxPools + 1
			if enabled {
				want--
			}
			if r.StatusCode != 200 || result.Opened != want || result.Refused != enabled {
				t.Fatalf("registration/driver side effects: %+v status=%d", result, r.StatusCode)
			}
		})
	}
}

func TestFunctionResourcesCustomHookOrdering(t *testing.T) {
	for _, readable := range []bool{false, true} {
		t.Run("readable="+strconv.FormatBool(readable), func(t *testing.T) {
			witness, gate := filepath.Join(t.TempDir(), "events"), filepath.Join(t.TempDir(), "customer")
			drain := filepath.Join(t.TempDir(), "drain")
			if err := os.WriteFile(drain, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			hook := `if(ctx.lifecycle_hook==='before_checkpoint') {
 const body=(async function*(){
   await databases[0].query('SELECT 1'); record('customer-start',0);
   while(!existsSync(process.env.CUSTOM_GATE))await new Promise(resolve=>setTimeout(resolve,5));
   yield 'internal'; record('customer-finished',0);
 })();
 return {body};
}
if(ctx.lifecycle_hook==='after_restore') {
 await databases[0].query('SELECT 1'); record('customer-after',0); return {statusCode:204};
}`
			if readable {
				hook = strings.Replace(hook, "return {body};", `return new Response(new ReadableStream({async pull(controller){
 const next=await body.next(); if(next.done)controller.close();else controller.enqueue(new TextEncoder().encode(next.value));
 }}));`, 1)
			}
			source := functionResourceFixture + strings.Replace(backgroundFunctionSource, "calls++;", "calls++;"+hook, 1)
			host := &executionWireHost{}
			f := startBackgroundFunction(t, api.PlanFree, 1, false, source, 0, executionWiring(host), resourceFixtureEnv(t, witness, drain), func(cfg *ServeConfig) { cfg.Spec.Env = append(cfg.Spec.Env, "CUSTOM_GATE="+gate) })
			f.get(t, "/state", "initial")
			awaitBackground(t, func() bool { return host.count() == 0 })
			reply := make(chan int, 1)
			go func() { reply <- resourceHook(f, "/custom-before", "before_checkpoint") }()
			awaitBackground(t, func() bool {
				data, _ := os.ReadFile(witness)
				return strings.Contains(string(data), ":customer-start:")
			})
			data, _ := os.ReadFile(witness)
			if strings.Contains(string(data), ":closing:") {
				t.Fatal("drained pools before custom hook body finished")
			}
			select {
			case <-reply:
				t.Fatal("acknowledged unfinished custom hook body")
			default:
			}
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case status := <-reply:
				if status != 204 {
					t.Fatalf("custom checkpoint status=%d", status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("custom checkpoint did not finish")
			}
			if status := resourceHook(f, "/custom-after", "after_restore"); status != 204 {
				t.Fatal("custom restore could not query rebuilt pools")
			}
			data, _ = os.ReadFile(witness)
			if strings.Count(string(data), ":closed:") != 2 || strings.Count(string(data), ":opened:") != 4 || !strings.Contains(string(data), ":customer-after:") {
				t.Fatal("custom lifecycle omitted pool ownership")
			}
		})
	}
}

func TestFunctionResourcesDrainEveryInterpreterBeforeAcknowledgement(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run("close-fails="+strconv.FormatBool(fail), func(t *testing.T) {
			host := &executionWireHost{}
			witness := filepath.Join(t.TempDir(), "pools")
			gate := filepath.Join(t.TempDir(), "drain")
			source := functionResourceFixture + backgroundFunctionSource
			f := startBackgroundFunction(t, api.PlanFree, 1, false, source, 0, executionWiring(host), func(cfg *ServeConfig) {
				data, err := templates.FS.ReadFile("rest-api-postgres/runtime/postgres-pool-runtime.js")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(cfg.Spec.Args[0]), "database-runtime.js"), data, 0o600); err != nil {
					t.Fatal(err)
				}
				cfg.Spec.Env = append(cfg.Spec.Env, "POOL_FIXTURE_WITNESS="+witness, "POOL_FIXTURE_DRAIN="+gate, "POOL_FIXTURE_FAIL="+strconv.FormatBool(fail))
				if _, err := workerpool.PrewarmIfSupported(t.Context(), cfg.Execution.WorkerSpec(cfg.Spec)); err != nil {
					t.Fatal(err)
				}
			})
			pids := map[int]bool{}
			for n := range 4 {
				pids[f.get(t, "/tail?mode=file", "held-"+strconv.Itoa(n)).PID] = true
			}
			if len(pids) != 4 {
				t.Fatal("fixture did not load four interpreters")
			}
			if err := os.WriteFile(f.release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			awaitBackground(t, func() bool { return host.count() == 0 })
			reply := make(chan int, 1)
			go func() { reply <- resourceHook(f, functionlifetime.BeforeResourcePath, "before_checkpoint") }()
			awaitBackground(t, func() bool {
				data, _ := os.ReadFile(witness)
				return strings.Count(string(data), ":closing:") == 8
			})
			select {
			case <-reply:
				t.Fatal("hook acknowledged before every pool drained")
			default:
			}
			if host.count() != 1 {
				t.Fatal("pool drainage lost VM ownership")
			}
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case status := <-reply:
				want := http.StatusNoContent
				if fail {
					want = http.StatusServiceUnavailable
				}
				if status != want {
					t.Fatalf("hook status=%d want=%d", status, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("drained hook did not complete")
			}
			awaitBackground(t, func() bool { return host.count() == 0 })
			if fail {
				return
			}
			data, _ := os.ReadFile(witness)
			if strings.Count(string(data), ":closed:") != 8 {
				t.Fatal("an interpreter pool was omitted")
			}
			if status := resourceHook(f, functionlifetime.BeforeResourcePath, "before_checkpoint"); status != 204 {
				t.Fatal("duplicate checkpoint failed")
			}
			if status := resourceHook(f, functionlifetime.AfterResourcePath, "after_restore"); status != 204 {
				t.Fatal("automatic restore failed")
			}
			data, _ = os.ReadFile(witness)
			if strings.Count(string(data), ":opened:") != 16 || strings.Count(string(data), ":probe:") != 8 {
				t.Fatal("restore did not rebuild and probe all pools")
			}
			f.get(t, "/state", "ready")
		})
	}
}

func resourceHook(f *backgroundFunction, path, kind string) int {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	header := "X-Faas-Before-Checkpoint"
	if kind == "after_restore" {
		header = "X-Faas-After-Restore"
	}
	req.Header.Set(header, "1")
	rec := httptest.NewRecorder()
	Serve(rec, req, f.cfg)
	return rec.Code
}

func TestFunctionResourcesRecoveryWaitsForEveryProbe(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run("probe-fails="+strconv.FormatBool(fail), func(t *testing.T) {
			witness, drain := filepath.Join(t.TempDir(), "events"), filepath.Join(t.TempDir(), "drain")
			gate := filepath.Join(t.TempDir(), "probes")
			if err := os.WriteFile(drain, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			source := strings.Replace(functionResourceFixture, "if(text==='SELECT 1')record('probe',this.id);", `if(text==='SELECT 1'){
 record('probing',this.id);
 if(process.env.PROBE_FAIL==='true'&&this.id===3)throw new Error('private-probe-failure');
 while(!existsSync(process.env.PROBE_GATE))await new Promise(resolve=>setTimeout(resolve,5));
 record('probe',this.id);
}`, 1) + backgroundFunctionSource
			host := &executionWireHost{}
			f := startBackgroundFunction(t, api.PlanFree, 1, false, source, 0, executionWiring(host), resourceFixtureEnv(t, witness, drain), func(cfg *ServeConfig) {
				cfg.Spec.Env = append(cfg.Spec.Env, "PROBE_GATE="+gate, "PROBE_FAIL="+strconv.FormatBool(fail))
			})
			for n := range 4 {
				f.get(t, "/tail?mode=file", "held-"+strconv.Itoa(n))
			}
			if err := os.WriteFile(f.release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			awaitBackground(t, func() bool { return host.count() == 0 })
			if resourceHook(f, functionlifetime.BeforeResourcePath, "before_checkpoint") != 204 {
				t.Fatal("checkpoint failed")
			}
			reply := make(chan int, 1)
			go func() { reply <- resourceHook(f, functionlifetime.AfterResourcePath, "after_restore") }()
			awaitBackground(t, func() bool {
				data, _ := os.ReadFile(witness)
				return strings.Count(string(data), ":probing:") == 8
			})
			select {
			case <-reply:
				t.Fatal("recovery returned while a neighbouring pool probe was unfinished")
			default:
			}
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case status := <-reply:
				want := 204
				if fail {
					want = 503
				}
				if status != want {
					t.Fatalf("probe result=%d want=%d", status, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("recovery did not finish after probes settled")
			}
		})
	}
}

const functionResourceFixture = `import {EventEmitter} from 'node:events';
import {appendFileSync,existsSync} from 'node:fs';
import {createPostgresPool} from './database-runtime.js';
let poolSerial=0;
const record=(phase,id)=>appendFileSync(process.env.POOL_FIXTURE_WITNESS,process.pid+':'+phase+':'+id+'\n');
class Pool extends EventEmitter {
 constructor(){super();this.id=++poolSerial;record('opened',this.id)}
 async connect(){return Object.assign(new EventEmitter(),{query:async(text)=>{
   if(text==='SELECT 1')record('probe',this.id);return {rows:[]};
 },release(){}})}
 async end(){record('closing',this.id);
   if(process.env.POOL_FIXTURE_FAIL==='true'&&this.id===1)throw new Error('private-driver-value');
   while(!existsSync(process.env.POOL_FIXTURE_DRAIN))await new Promise(resolve=>setTimeout(resolve,5));
   record('closed',this.id);
 }
}
const databases=[createPostgresPool(()=>new Pool()),createPostgresPool(()=>new Pool())];
`

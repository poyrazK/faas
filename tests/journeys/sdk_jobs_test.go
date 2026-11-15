package journeys

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestSDKTypedBackgroundJobs uses an explicitly configured isolated job image
// running examples/sdk-csv-jobs/worker with its matching generated contract,
// PostgreSQL schema and managed obj:// result adapter. It never edits the job.
func TestSDKTypedBackgroundJobs(t *testing.T) {
	tg := needs(t, tierFull, true)
	module, contract := os.Getenv("GREGALE_E2E_NODE_SDK"), os.Getenv("GREGALE_E2E_JOB_CONTRACT")
	if module == "" || contract == "" || tg.token == "" {
		t.Skip("set GREGALE_E2E_NODE_SDK and GREGALE_E2E_JOB_CONTRACT for an isolated typed CSV job")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node 22+ required")
	}
	module, err = filepath.Abs(module)
	if err != nil {
		t.Fatal(err)
	}
	contract, err = filepath.Abs(contract)
	if err != nil {
		t.Fatal(err)
	}
	const driver = `import {readFile} from 'node:fs/promises';import {pathToFileURL} from 'node:url';
const sdk=await import(pathToFileURL(process.env.SDK_MODULE).href);
const contract=new sdk.JobContract(JSON.parse(await readFile(process.env.JOB_CONTRACT,'utf8')));
const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN}),job=client.jobs.defineJob(contract);
const options={timeoutMs:900000,maxPolls:900,pollDelayMs:1000};const runs=[];
try {
 const id=process.env.IMPORT_ID;
 const source=await job.submit([{id:id+':1',value:{row:1,email:'one@example.test'}},{id:id+':2',value:{row:2,email:'two@example.test',fail_once:true}},{id:id+':3',value:{row:3,email:'invalid email'}}],{...options,idempotencyKey:id,parallelism:2,retryMax:0});runs.push(source);
 const resumed=job.resume(sdk.parseJobCheckpoint(JSON.parse(JSON.stringify(source.checkpoint))));
 if((await resumed.wait(options)).status!=='failed')throw new Error('expected transient task failure');
 const items=await resumed.results(options);if(items[0].outcome!=='succeeded'||items[1].outcome!=='failed'||items[2].result?.rejection!=='invalid_email')throw new Error('unexpected item outcomes');
 const replay=await resumed.retryFailed({...options,idempotencyKey:id+'-retry'});runs.push(replay);
 if(replay.checkpoint.inputs.length!==1||replay.checkpoint.inputs[0].input_id!==id+':2')throw new Error('replay selected successful input');
 if((await replay.wait(options)).status!=='succeeded'||(await replay.results(options))[0].result?.saved!==true)throw new Error('recovery failed');
 console.log(JSON.stringify({outcome:'succeeded',source:source.checkpoint.run_id,replay:replay.checkpoint.run_id}));
} catch(error) { console.error(JSON.stringify({code:error.code??'journey_failed'}));process.exitCode=1; }
finally {for(const run of runs){try{if(['queued','running'].includes((await run.status({timeoutMs:10000})).status))await run.cancel({timeoutMs:10000});}catch{}}}`
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", driver)
	cmd.Env = append(tg.cliEnv(tg.token), "FAAS_API="+tg.api, "SDK_MODULE="+module, "JOB_CONTRACT="+contract, "IMPORT_ID="+tg.slug("csv"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("typed job journey: %v %s", err, tg.redact(tail(string(output), 2000)))
	}
	if str(object(t, output), "outcome") != "succeeded" {
		t.Fatalf("unexpected job outcome: %s", tg.redact(string(output)))
	}
}

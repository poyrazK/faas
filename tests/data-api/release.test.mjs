import test from 'node:test'
import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdtemp, cp, readFile, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const execute=promisify(execFile)
const enabled=process.env.DATA_API_VERIFY_NATIVE && process.env.DATA_API_DEV_CLI && process.env.DATA_API_STARTER_DIR
test('release checks aggregate real checks, stop on budgets and coverage failures and preserve artifacts', {skip:!enabled,timeout:360000},async t=>{
 const directory=await mkdtemp(join(tmpdir(),'gregale-data-release-'));t.after(()=>rm(directory,{recursive:true,force:true}))
 await cp(process.env.DATA_API_STARTER_DIR,directory,{recursive:true})
 const cli=process.env.DATA_API_DEV_CLI
 const inventory=async args=>(await execute('docker',args)).stdout.trim().split('\n').filter(Boolean).sort()
 const containers=()=>inventory(['container','ls','--all','--filter','label=gregale.data-api.dev=true','--format','{{.Names}}'])
 const volumes=()=>inventory(['volume','ls','--format','{{.Name}}'])
 const initialContainers=await containers(),initialVolumes=await volumes()
 await execute(cli,['data-api','dev','--directory',directory,'--once','--port','0'],{timeout:60000})
 const schema=await readFile(join(directory,'.gregale/data-api-contract.json'),'utf8')
 const types=await readFile(join(directory,'client/src/database.types.ts'),'utf8')
 await writeFile(join(directory,'schema.json'),schema)
 await writeFile(join(directory,'checks.json'),JSON.stringify({version:1,checks:[{name:'No null bodies',sql:'SELECT NOT EXISTS(SELECT 1 FROM api.notes WHERE body IS NULL) AS passed'}]}))
 await writeFile(join(directory,'access.json'),JSON.stringify({version:1,requests:[{name:'Bob sees no Alice rows',identity:'bob',method:'GET',path:'/rest/v1/notes?subject=eq.alice&select=id',expect:{status:200,json:[]}}]}))
 await writeFile(join(directory,'requests.json'),JSON.stringify({version:1,requests:[{name:'Read query',method:'GET',path:'/rest/v1/notes?select=id',identity:'alice',expect:{status:200,json:[]}}]}))
 const plans=JSON.parse((await execute(cli,['--json','data-api','explain','--directory',directory,'--collection','requests.json'],{timeout:60000})).stdout)
 await writeFile(join(directory,'plans.json'),JSON.stringify(plans))
 await writeFile(join(directory,'budget.json'),JSON.stringify({version:1,queries:[{scenario:'default',name:'Read query',max_total_cost:plans.results[0].plan.total_cost,max_cost_ratio:1}]}))
 const current=JSON.parse(schema)
 const policy={version:1,default_required:['integrity'],relations:current.snapshot.tables.map(table=>({relation:`${table.schema}.${table.name}`,required:[],checks:[]}))}
 const notes=policy.relations.find(item=>item.relation==='api.notes');notes.required=['integrity','access_read','performance'];notes.checks=[{kind:'integrity',name:'No null bodies'},{kind:'access_read',scenario:'default',name:'Bob sees no Alice rows'},{kind:'performance',scenario:'default',name:'Read query'}]
 await writeFile(join(directory,'coverage-policy.json'),JSON.stringify(policy))
 const config={version:1,baseline:'schema.json',current_schema:'schema.json',checks:'checks.json',access_checks:'access.json',collection:'requests.json',plan_baseline:'plans.json',budget:'budget.json',coverage_policy:'coverage-policy.json'}
 await writeFile(join(directory,'release.json'),JSON.stringify(config))
 const args=['--json','data-api','verify','release','--config',join(directory,'release.json')]
 const passed=JSON.parse((await execute(cli,args,{timeout:180000,maxBuffer:4<<20})).stdout)
 assert.equal(passed.status,'passed');assert.equal(passed.cleanup,'passed');assert.ok(passed.steps.every(step=>step.status==='passed'))
 notes.required.push('access_admin');await writeFile(join(directory,'coverage-policy.json'),JSON.stringify(policy))
 await assert.rejects(execute(cli,args,{timeout:180000,maxBuffer:4<<20}),error=>{const report=JSON.parse(error.stdout);assert.equal(report.status,'failed');assert.equal(report.steps[3].status,'failed');assert.equal(report.steps[3].report.gaps,1);assert.equal(report.cleanup,'passed');return true})
 notes.required.pop();await writeFile(join(directory,'coverage-policy.json'),JSON.stringify(policy))
 await writeFile(join(directory,'budget.json'),JSON.stringify({version:1,queries:[{scenario:'default',name:'Read query',max_total_cost:0}]}))
 await assert.rejects(execute(cli,args,{timeout:180000,maxBuffer:4<<20}),error=>{const report=JSON.parse(error.stdout);assert.equal(report.steps[2].status,'failed');assert.equal(report.steps[3].status,'blocked');assert.equal(report.cleanup,'passed');return true})
 assert.equal(await readFile(join(directory,'.gregale/data-api-contract.json'),'utf8'),schema)
 assert.equal(await readFile(join(directory,'client/src/database.types.ts'),'utf8'),types)
 assert.deepEqual(await containers(),initialContainers);assert.deepEqual(await volumes(),initialVolumes)
})

import test from 'node:test'
import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdtemp, cp, readFile, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const execute=promisify(execFile)
const enabled=process.env.DATA_API_VERIFY_NATIVE && process.env.DATA_API_DEV_CLI && process.env.DATA_API_STARTER_DIR
test('verification init generates runnable SQL checks and deliberately failing access drafts', {skip:!enabled,timeout:240000},async t=>{
 const directory=await mkdtemp(join(tmpdir(),'gregale-verify-init-'));t.after(()=>rm(directory,{recursive:true,force:true}))
 await cp(process.env.DATA_API_STARTER_DIR,directory,{recursive:true})
 const cli=process.env.DATA_API_DEV_CLI
 await execute(cli,['data-api','dev','--directory',directory,'--once','--port','0'],{timeout:60000})
 const output=join(directory,'verification'),schema=join(directory,'.gregale/data-api-contract.json')
 const original=await readFile(schema,'utf8')
 const initArgs=['--json','data-api','verify','init','--schema',schema,'--config',join(directory,'data-api.dev.json'),'--output',output]
 const collection=join(directory,'budget-requests.json'),plans=join(directory,'plans.json')
 await writeFile(collection,JSON.stringify({version:1,requests:[{name:'Concrete read',method:'GET',path:'/rest/v1/notes?select=id',identity:'alice',expect:{status:200,json:[]}}]}))
 const explainArgs=['--json','data-api','explain','--directory',directory,'--collection',collection]
 const explained=await execute(cli,explainArgs,{timeout:60000});await writeFile(plans,explained.stdout)
 initArgs.push('--plans',plans)
 const initialized=JSON.parse((await execute(cli,initArgs)).stdout);assert.equal(initialized.status,'needs_review')
 const coverage=JSON.parse(await readFile(join(output,'coverage.json'),'utf8'));assert.ok(coverage.relations.every(item=>item.access!=='verified'))
 const args=['--json','data-api','verify','--directory',directory,'--checks',join(output,'data-checks.json')]
 const verified=JSON.parse((await execute(cli,args,{timeout:60000})).stdout);assert.equal(verified.status,'passed')
 const verificationReport=join(directory,'verified.json');await writeFile(verificationReport,JSON.stringify(verified))
 await assert.rejects(execute(cli,[...args,'--access-checks',join(output,'access-checks.json')],{timeout:60000}),error=>{const report=JSON.parse(error.stdout);assert.equal(report.status,'failed');assert.equal(report.cleanup,'passed');assert.ok(report.access_summary.failed);return true})
 const checked=JSON.parse((await execute(cli,[...explainArgs,'--baseline',plans,'--check','--budget',join(output,'budgets.json')],{timeout:60000})).stdout);assert.equal(checked.budget_check.status,'passed')
 const checkedReport=join(directory,'checked-plans.json');await writeFile(checkedReport,JSON.stringify(checked))
 const policyFile=join(directory,'coverage-policy.json')
 const policy={version:1,default_required:['integrity'],relations:coverage.relations.map(item=>({relation:item.relation,required:[],checks:[]}))}
 policy.relations.find(item=>item.relation==='api.notes').required=['integrity','performance']
 policy.relations.find(item=>item.relation==='api.notes').checks=[{kind:'integrity',name:verified.checks[0].name},{kind:'performance',scenario:'default',name:'Concrete read'}]
 await writeFile(policyFile,JSON.stringify(policy))
 const coverageArgs=['--json','data-api','verify','coverage','--schema',schema,'--policy',policyFile,'--verification',verificationReport,'--plans',checkedReport,'--check']
 const passedCoverage=JSON.parse((await execute(cli,coverageArgs)).stdout);assert.equal(passedCoverage.status,'passed')
 policy.relations.find(item=>item.relation==='api.notes').required.push('access_admin')
 await writeFile(policyFile,JSON.stringify(policy))
 await assert.rejects(execute(cli,coverageArgs),error=>{const report=JSON.parse(error.stdout);assert.equal(error.code,1);assert.equal(report.status,'missing_coverage');assert.equal(report.gaps,1);return true})

 await assert.rejects(execute(cli,initArgs))
 assert.equal(await readFile(schema,'utf8'),original)
})

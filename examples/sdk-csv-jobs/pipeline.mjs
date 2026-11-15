import {readFile} from 'node:fs/promises';
/** Construct once outside workflow invocations. Native workflow dispatch pins
 * the deployment. The application authenticates owner_id before starting it. */
export async function csvPipeline({sdk,client,scope,store,jobStore,gateStore,reviewed=false,gateTimeoutMs=60000}){
  const contract=new sdk.JobContract(JSON.parse(await readFile(new URL(reviewed?'reviewed-pipeline-contract.json':'pipeline-contract.json',import.meta.url),'utf8')));
  const reviewContract=reviewed?new sdk.JobContract(JSON.parse(await readFile(new URL('review-contract.json',import.meta.url),'utf8'))):null;
  const jobs={};
  for(const [step,file]of [['validate','validate-contract.json'],['import','contract.json'],['report','report-contract.json']]){
    const c=new sdk.JobContract(JSON.parse(await readFile(new URL(file,import.meta.url),'utf8')));
    jobs[step]={contract:c,job:client.jobs.defineJob(c,{store:jobStore}),jobStore,parallelism:2,retryMax:0};
  }
  const pipeline=sdk.defineJobPipeline(contract,{scope,store,jobs,gateStore,pollDelayMs:1000,maxPolls:100},async(input,wf)=>{
    if(input.source.owner_id!==wf.ownerId||input.source.scope.base_url!==scope.base_url||input.source.scope.app_slug!==scope.app_slug)throw new sdk.PipelineError('scope_mismatch');
    const validated=await wf.job('validate',[{id:input.import_id,value:{source:input.source,import_id:input.import_id}}]);
    if(reviewed){
      const outcome=await wf.approval('review',reviewContract,{source:input.source,rows:validated.results[0].result.rows},{timeoutMs:gateTimeoutMs,requiredRoles:['reviewer']});
      if(outcome.state!=='approved')return {status:outcome.state,saved:0,rejected:0};
    }
    const imported=await wf.job('import',validated.results[0].result.rows);
    const rejections=imported.results.filter(r=>r.result.rejection==='invalid_email').map(r=>r.input_id);
    const report=await wf.job('report',[{id:'report:'+input.import_id,value:{import_id:input.import_id,owner_id:wf.ownerId,rejections}}]);
    return {...(reviewed?{status:'completed'}:{}),report:report.results[0].result,saved:imported.results.filter(r=>r.result.saved).length,rejected:rejections.length};
  });
  return {contract,pipeline,reviewContract};
}

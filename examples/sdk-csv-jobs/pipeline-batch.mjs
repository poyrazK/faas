import {readFile} from 'node:fs/promises';
/** Publish this definition together with csv-reviewed-pipeline. Authentication
 * supplies owner_id. Start/inspect use the same generated root contract. */
export async function csvBatchPipeline({sdk,scope,token,store,childrenStore,gateStore,requestTimeoutMs=10000}){
  const contract=new sdk.JobContract(JSON.parse(await readFile(new URL('batch-contract.json',import.meta.url),'utf8')));
  const child={contract:new sdk.JobContract(JSON.parse(await readFile(new URL('reviewed-pipeline-contract.json',import.meta.url),'utf8')))};
  const coordinator=new sdk.PipelineChildren({scope,parentContract:contract,children:[child],store:childrenStore,token,gateStore,requestTimeoutMs});
  const pipeline=sdk.defineJobPipeline(contract,{scope,store,jobs:{},children:coordinator,requestTimeoutMs,pollDelayMs:1000,maxPolls:100},async(input,wf)=>{
    const result=await wf.children('imports',child,input.imports,{timeoutMs:180000,concurrency:2,failurePolicy:'collect',onParentStopped:'cancel_children',onDeadline:'cancel_children'});
    return {saved:result.children.reduce((n,c)=>n+(c.output?.saved??0),0),rejected:result.children.reduce((n,c)=>n+(c.output?.rejected??0),0),children:result.children.map(c=>({id:c.id,phase:c.phase,...(c.run_id?{run_id:c.run_id}:{}),...(c.output?{status:c.output.status,...(c.output.report?{report:c.output.report}:{})}:{})}))};
  });
  return {contract,child,coordinator,pipeline};
}

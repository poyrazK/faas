import {readFile} from 'node:fs/promises';

/** The host authenticates actor; trusted immutable records bind each request to its input. */
export async function csvPipelineTool({sdk,scope,store,gateStore,token,reviewBaseURL,loadImport,valueHash}) {
  const hash=valueHash??(await import('@gregale/sdk-node/jobs/runtime')).jobValueHash;
  const contract=new sdk.JobContract(JSON.parse(await readFile(new URL('reviewed-pipeline-contract.json',import.meta.url),'utf8')));
  return sdk.definePipelineTool(contract,{
    name:'csv_import',description:'Validate, review and import an uploaded CSV, then produce a report.',
    scope,store,gateStore,token,reviewBaseURL,
    authorize:async(request,actor)=>{
      const record=await loadImport(request.request_id);
      if(!record?.enabled||record.owner_id!==actor)return null;
      if(request.input!==undefined){
        if(hash(contract.input(request.input))!==hash(contract.input(record.input)))return null;
      }
      return {subject_id:actor,owner_id:record.owner_id};
    },
  });
}

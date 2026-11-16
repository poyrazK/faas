/** Authenticated owner configuration, deployed as workflow.trigger. */
export function csvPipelineSchedule({sdk,contract,scope,input,ownerId,timezone='UTC'}){
  if(input.source.owner_id!==ownerId||input.source.scope.base_url!==scope.base_url||input.source.scope.app_slug!==scope.app_slug)throw new Error('invalid_import_owner');
  return sdk.definePipelineSchedule(contract,input,{scope,ownerId,cron:'0 2 * * *',timezone,overlap:'skip',catchUp:'skip'});
}

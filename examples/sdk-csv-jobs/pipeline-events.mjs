import {readFile} from 'node:fs/promises';

/** A verified event names an immutable business record. It cannot claim an owner
 * or supply an arbitrary file. Authentication and current role checks belong to
 * the application's verify/authorizeOwner callbacks, including on recovery. */
export async function csvEventPipeline({sdk,scope,subscriptionId,source,contract,store,token,verify,loadImport,authorizeOwner}) {
  const spec=JSON.parse(await readFile(new URL('event-contract.json',import.meta.url),'utf8'));
  return sdk.createEventPipeline(new sdk.EventContract(spec),{
    name:'csv-import-events',scope,subscriptionId,source,contract,store,token,verify,
    authorize:async(event,access,action,signal)=>{
      const record=await loadImport(event.data.import_id,signal);
      if(!record||!await authorizeOwner(record.owner_id,access,action,signal))return null;
      return record.owner_id;
    },
    map:async(event,ownerId,signal)=>{
      const record=await loadImport(event.data.import_id,signal);
      if(!record||record.owner_id!==ownerId||record.input.import_id!==event.data.import_id)throw new Error('import_changed');
      return record.input;
    },
  });
}

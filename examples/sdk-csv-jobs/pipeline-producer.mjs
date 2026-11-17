import {readFile} from 'node:fs/promises';

/** Call after authenticating the owner and checking the uploaded source binding. */
export async function stageCsvImport({sdk,pool,scope,importId,ownerId,input,signal}) {
  if(input.import_id!==importId||input.source.owner_id!==ownerId)throw new Error('invalid_import_owner');
  const contract=new sdk.EventContract(JSON.parse(await readFile(new URL('event-contract.json',import.meta.url),'utf8')));
  const publisher=sdk.createEventPublisher(contract,{scope});
  return sdk.withEventOutboxTransaction(pool,async tx=>{
    await tx.query('INSERT INTO sdk_csv_event_inputs VALUES ($1,$2,$3)',[importId,ownerId,input]);
    return publisher.stage(tx,{import_id:importId},{key:'csv-import:'+importId});
  },{timeoutMs:10000,signal});
}

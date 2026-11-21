import {readFile} from 'node:fs/promises';

/** verify must authenticate the private platform boundary before reading headers. */
export async function paidCsvAdapter({sdk,pool,scope,endpointId,verify}) {
  const contract=new sdk.EventContract(JSON.parse(await readFile(new URL('event-contract.json',import.meta.url),'utf8')));
  return sdk.createStripeWebhookAdapter(contract,{
    name:'paid-csv-imports',scope,endpointId,deliveryPath:'/webhooks/stripe',apiVersion:'2024-06-20',livemode:false,
    eventTypes:['payment_intent.succeeded','payment_intent.payment_failed'],pool,token:process.env.FAAS_TOKEN??'fixture-owner',verify,
    authorize:async(tx,event,actor)=>{
      const {rows:[row]}=await tx.query('SELECT * FROM sdk_csv_payments WHERE payment_id=$1 FOR UPDATE',[event.data_object.id]);
      return row?.enabled&&row.owner_id===actor&&row.customer_id===event.data_object.customer?row.owner_id:null;
    },
    apply:async(tx,event)=>{
      const {rows:[row]}=await tx.query('SELECT * FROM sdk_csv_payments WHERE payment_id=$1',[event.data_object.id]);
      if(row.state==='cancelled')return {state:'ignore',reason:'cancelled'};
      if(row.state==='paid')return {state:'ignore',reason:'already_applied'};
      if(event.type!=='payment_intent.succeeded')return {state:'ignore',reason:'not_applicable'};
      const payment=event.data_object;
      if(payment.object!=='payment_intent'||payment.status!=='succeeded'||payment.amount_received!==row.amount_cents||payment.currency!==row.currency)throw new Error('payment_mismatch');
      if(row.input.import_id!==row.import_id||row.input.source.owner_id!==row.owner_id)throw new Error('invalid_import_owner');
      await tx.query("UPDATE sdk_csv_payments SET state='paid',applications=applications+1 WHERE payment_id=$1",[payment.id]);
      await tx.query('INSERT INTO sdk_csv_event_inputs VALUES ($1,$2,$3)',[row.import_id,row.owner_id,row.input]);
      return {state:'stage',input:{import_id:row.import_id}};
    },
  });
}

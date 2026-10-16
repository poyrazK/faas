import pg from 'pg'
import {createHash} from 'node:crypto'
const quote=value=>'"'+value.replaceAll('"','""')+'"'
// Private to disposable verification. Owner reads are never returned in reports.
export function mutationObserver(connectionString,snapshot,signal){
 const tables=snapshot.tables.filter(table=>!table.view)
 if(!tables.length||tables.length>64)throw new Error('Mutation observation exceeds the table bound.')
 return async fixture=>{
  signal?.throwIfAborted()
  const client=new pg.Client({connectionString,connectionTimeoutMillis:5000});const states=[];let rows=[],bytes=0
  try{
   await client.connect();await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY');await client.query("SET LOCAL statement_timeout='5s'");await client.query("SET LOCAL lock_timeout='1s'")
   for(const table of tables){
    signal?.throwIfAborted()
    const result=await client.query(`SELECT CASE WHEN octet_length(to_jsonb(t)::text)>65536 THEN NULL ELSE to_jsonb(t)::text END AS value FROM ${quote(table.schema)}.${quote(table.name)} t LIMIT 1001`)
    if(result.rows.length>1000||result.rows.some(row=>row.value===null))throw new Error('Mutation observation exceeds row bounds.')
    const values=result.rows.map(row=>row.value).sort()
    bytes+=Buffer.byteLength(JSON.stringify(values));if(bytes>1048576)throw new Error('Mutation observation exceeds byte bounds.')
    states.push([table.schema,table.name,values])
    if(table.schema===fixture.schema&&table.name===fixture.table)rows=values.map(value=>JSON.parse(value)).filter(row=>Object.entries(fixture.keys).every(([key,value])=>String(row[key])===String(value)))
   }
   await client.query('ROLLBACK')
   return {signature:createHash('sha256').update(JSON.stringify(states)).digest('hex'),rows}
  }finally{await client.end().catch(()=>{})}
 }
}

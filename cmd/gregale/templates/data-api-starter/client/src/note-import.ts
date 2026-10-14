import * as contract from './database.types.js'
import type { Database } from './database.types.js'
import { parseCsv } from './csv.js'

type Insert = Database['api']['Tables']['notes']['Insert']
type Issue = Readonly<{row:number;code:string;path:readonly (string|number)[]}>
type Preview = Readonly<{ok:true;rows:readonly Insert[];total:number;valid:number;invalid:0;errors:readonly Issue[];truncated:false}> |
 Readonly<{ok:false;total:number;valid:number;invalid:number;errors:readonly Issue[];truncated:boolean}>
const generated = contract as unknown as {
 inputValidators?: {api:{notes:{insert(input:unknown):Insert}}}
 validateInputRows?: (rows:readonly unknown[],parse:(input:unknown)=>Insert,options:{maxErrors?:number})=>Preview
}
/** Pure preview. Subject comes from application context, never mapped CSV data. */
export function previewNotesCsv(source:string,options:{subject:string;body?:string;priority?:string;maxErrors?:number}):Preview {
 if(!generated.inputValidators||!generated.validateInputRows) throw new Error('Regenerate types with --validators before previewing notes CSV')
 if(!options.subject) throw new Error('Choose a signed-in subject')
 const [headers,...records]=parseCsv(source)
 if(!records.length) throw new Error('CSV has no data records')
 const body=headers.indexOf(options.body??'body'),priority=options.priority===undefined?headers.indexOf('priority'):headers.indexOf(options.priority)
 if(body<0||options.priority!==undefined&&priority<0||body===priority) throw new Error('Map distinct body and optional priority columns')
 const rows=records.map(record=>{
  if(record.length!==headers.length) return null
  const value:Record<string,unknown>={subject:options.subject,body:record[body]}
  if(priority>=0&&record[priority].trim()) {
   const raw=record[priority].trim()
   value.priority=/^[+-]?\d+(?:\.\d+)?$/.test(raw)?Number(raw):NaN
  }
  return value
 })
 const result=generated.validateInputRows(rows,generated.inputValidators.api.notes.insert,{maxErrors:options.maxErrors})
 return Object.freeze({...result,errors:Object.freeze(result.errors.map(error=>Object.freeze({...error,row:error.row+1})))}) as Preview
}

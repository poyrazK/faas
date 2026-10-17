import type { BindingVerificationRun } from './generated/models/BindingVerificationRun.js';
import type { ProjectBindingVerificationRun } from './generated/models/ProjectBindingVerificationRun.js';
import type { BindingVerificationRunDiagnostics } from './generated/models/BindingVerificationRunDiagnostics.js';
import type { ProjectBindingVerificationRunDiagnostics } from './generated/models/ProjectBindingVerificationRunDiagnostics.js';
export type BindingVerificationDiagnostics = BindingVerificationRunDiagnostics | ProjectBindingVerificationRunDiagnostics;
const object=(value:unknown):Record<string,unknown>=>{if(!value||typeof value!=='object'||Array.isArray(value))throw new Error('invalid diagnostics');return value as Record<string,unknown>;};
const pick=(value:unknown,keys:string[]):Record<string,unknown>=>{const source=object(value);return Object.fromEntries(keys.filter(key=>key in source).map(key=>[key,source[key]]));};
const date=(value:unknown):number=>typeof value==='string'?Date.parse(value):NaN;
const canonicalId=(value:unknown):string=>typeof value==='string'?value.replaceAll('-',''):'';
const appFields=['run_id','graph_run_id','app','deployment_id','scope','status','checked_at','expires_at','complete','check_passed'];
function fields(raw:Record<string,unknown>):Record<string,unknown> {
 const out:Record<string,unknown>={};
 for(const [name,keys] of [
  ['blockers',['code','message','recovery','app','type','name','binding','scope','step_index','resource_id','resource_kind','status']],
  ['changes',['app','type','name','binding','scope','field','reviewed','current']],
  ['recovery',['action','message']],
 ] as const) {
  if(!Array.isArray(raw[name]))throw new Error('invalid diagnostic list');
  out[name]=(raw[name] as unknown[]).map(value=>pick(value,[...keys]));
 }
 if(raw.budget!==undefined)out.budget=pick(raw.budget,['run_id','max_concurrency','active_resources','waiting_steps','full']);
 return out;
}
export function appDiagnostics(value:unknown,app:string,id:string,original?:BindingVerificationRun):BindingVerificationRunDiagnostics {
 const raw=object(value);
 if(raw.run_id!==id||raw.app!==app||!Number.isFinite(date(raw.checked_at))||!Number.isFinite(date(raw.expires_at))||typeof raw.complete!=='boolean'||typeof raw.scope!=='string'||!raw.scope||!/^[a-f0-9]{32}$/.test(canonicalId(raw.deployment_id)))throw new Error('unconfirmed diagnostics');
 if(original&&(canonicalId(raw.deployment_id)!==canonicalId(original.deployment_id)||raw.scope!==original.scope||(raw.graph_run_id??'')!==(original.graph_run_id??'')||date(raw.expires_at)!==date(original.expires_at)))throw new Error('changed diagnostic selection');
 return {...pick(raw,appFields),...fields(raw)} as BindingVerificationRunDiagnostics;
}
export function projectDiagnostics(value:unknown,project:string,environment:string,id:string,original?:ProjectBindingVerificationRun):ProjectBindingVerificationRunDiagnostics {
 const raw=object(value),members=object(raw.members);
 if(raw.run_id!==id||raw.project!==project||raw.environment!==environment||!Number.isFinite(date(raw.checked_at))||!Number.isFinite(date(raw.expires_at))||typeof raw.complete!=='boolean')throw new Error('unconfirmed graph diagnostics');
 if(original&&(date(raw.expires_at)!==date(original.expires_at)||Object.keys(members).length!==Object.keys(original.members).length||Object.keys(original.members).some(name=>!(name in members))))throw new Error('changed diagnostic graph');
 const observed:Record<string,BindingVerificationRunDiagnostics>={};
 for(const [name,value] of Object.entries(members)) {
  const member=object(value),expected=original?.members[name];
  if(member.graph_run_id!==id||member.scope!==environment||date(member.expires_at)!==date(raw.expires_at)||typeof member.run_id!=='string')throw new Error('changed diagnostic member');
  observed[name]=appDiagnostics(member,name,expected?.id??member.run_id,expected);
 }
 return {...pick(raw,['run_id','project','environment','status','checked_at','expires_at','complete']),...fields(raw),members:observed} as ProjectBindingVerificationRunDiagnostics;
}

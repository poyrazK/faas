export interface IssueBreadcrumbEntry {
  occurred_at: string;
  category: 'http' | 'db' | 'cache' | 'workflow' | 'log' | 'custom';
  level: 'debug' | 'info' | 'warning' | 'error';
  message: string;
}
export interface IssueContextSnapshot {
  readonly breadcrumbs: readonly IssueBreadcrumbEntry[];
  readonly breadcrumbs_dropped: number;
  readonly tags: Readonly<Record<string, string>>;
}
export const issueContextLimits = Object.freeze({breadcrumbs:16,messageBytes:256,tags:16,keyBytes:64,valueBytes:128});
export const issueTagKeys = Object.freeze(['component','operation','feature_flag','feature_variant','dependency','region',
  'job_type','workflow','state','error_class','release_channel','runtime']);

function text(value: string, limit: number): string {
  const bytes=Buffer.from(value,'utf8');let end=Math.min(bytes.length,limit);
  while(end<bytes.length&&(bytes[end]!&0xc0)===0x80)end--;
  return bytes.subarray(0,end).toString('utf8');
}

/** Create per request or job. Nothing is collected automatically or shared
 * across operations. Only explicit messages and bounded string tags are kept. */
export function createIssueContext(options: {clock?:()=>Date} = {}) {
  const clock=options.clock??(()=>new Date());
  const breadcrumbs: IssueBreadcrumbEntry[]=[];
  const tags: Record<string,string>=Object.create(null) as Record<string,string>;
  let dropped=0;
  return {
    addBreadcrumb(input:{message:string;category?:IssueBreadcrumbEntry['category'];level?:IssueBreadcrumbEntry['level']}): boolean {
      const category=input.category??'custom',level=input.level??'info',now=clock();
      if(typeof input.message!=='string'||input.message.includes('\0')||!['http','db','cache','workflow','log','custom'].includes(category)||
          !['debug','info','warning','error'].includes(level)||!Number.isFinite(now.getTime()))return false;
      if(breadcrumbs.length===issueContextLimits.breadcrumbs){breadcrumbs.shift();dropped=Math.min(2147483647,dropped+1);}
      breadcrumbs.push({occurred_at:now.toISOString(),category,level,message:text(input.message,issueContextLimits.messageBytes)});
      return true;
    },
    setTag(key:string,value:string): boolean {
      if(typeof key!=='string'||typeof value!=='string'||value.includes('\0')||! /^[a-z][a-z0-9_.-]{0,63}$/.test(key)||
          !issueTagKeys.includes(key))return false;
      if(!(key in tags)&&Object.keys(tags).length===issueContextLimits.tags)return false;
      tags[key]=text(value,issueContextLimits.valueBytes);return true;
    },
    snapshot():IssueContextSnapshot {
      return Object.freeze({breadcrumbs:Object.freeze(breadcrumbs.map(entry=>Object.freeze({...entry}))),
        breadcrumbs_dropped:dropped,tags:Object.freeze({...tags})});
    },
  };
}

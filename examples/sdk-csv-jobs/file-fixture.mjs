/** Local protocol fixture; never evidence of deployed storage acceptance. */
import {randomUUID,createHash} from 'node:crypto';
export function fileStorageFixture(endpoint){
  const app='11111111-1111-4111-8111-111111111111',bucket='22222222-2222-4222-8222-222222222222';
  const sessions=new Map(),capabilities=new Map(),objects=new Map(),receipts=new Map();let issued=0;
  const sha=data=>createHash('sha256').update(data).digest('hex');
  const sign=(key,method,extra={})=>{const token=randomUUID();capabilities.set(token,{key,method,...extra});issued++;return {method,url:endpoint()+'/file-data/'+token,headers:{'Content-Type':'text/csv'},expires_at:'2099-01-01T00:00:00Z',...(extra.receipt?{upload_id:extra.receipt}:{})};};
  const project=s=>Object.fromEntries(Object.entries(s).filter(([key])=>key!=='parts'));
  const send=(res,status,value)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(value===undefined?'':JSON.stringify(value));};
  return {app,bucket,objects,counts:()=>({issued}),signArtifact(uri){const key=uri.slice(`obj://${app}/${bucket}/`.length);if(!uri.startsWith(`obj://${app}/${bucket}/`)||!objects.has(key))throw new Error('Unknown fixture artifact');return sign(key,'GET');},async handle(req,res){
    const u=new URL(req.url,'http://localhost');
    if(u.pathname.startsWith('/file-data/')){
      const capability=capabilities.get(u.pathname.split('/').at(-1));
      res.setHeader('Access-Control-Allow-Origin','*');res.setHeader('Access-Control-Allow-Methods','GET,HEAD,PUT,OPTIONS');res.setHeader('Access-Control-Allow-Headers','content-type');res.setHeader('Access-Control-Expose-Headers','ETag,Content-Length');
      if(req.method==='OPTIONS'){res.writeHead(204);res.end();return true;}
      if(!capability||capability.method!==req.method){send(res,403,{});return true;}
      if(req.headers.authorization||req.headers.cookie){send(res,400,{});return true;}
      if(req.method==='PUT'){
        const chunks=[];let size=0;for await(const chunk of req){size+=chunk.length;if(size>1024*1024){send(res,413,{});return true;}chunks.push(chunk);}const data=Buffer.concat(chunks),etag='"'+sha(data)+'"';
        if(capability.part){const session=sessions.get(capability.upload);if(session.state!=='active'){send(res,409,{});return true;}session.parts.set(capability.part,{data,etag});}
        else{if(capability.ifNoneMatch&&objects.has(capability.key)){send(res,412,{});return true;}objects.set(capability.key,data);Object.assign(receipts.get(capability.receipt),{status:'completed',bytes:data.length,etag});}
        res.writeHead(200,{ETag:etag});res.end();return true;
      }
      const data=objects.get(capability.key);if(!data){send(res,404,{});return true;}res.writeHead(200,{'Content-Type':'text/csv','Content-Length':data.length,ETag:'"'+sha(data)+'"'});res.end(req.method==='HEAD'?undefined:data);return true;
    }
    if(!u.pathname.startsWith('/v1/apps/imports'))return false;
    if(req.headers.authorization!=='Bearer fixture-owner'){send(res,403,{});return true;}
    if(u.pathname==='/v1/apps/imports'){send(res,200,{id:app});return true;}
    if(!u.pathname.startsWith(`/v1/apps/imports/buckets/${bucket}/`)){send(res,404,{});return true;}
    const path=u.pathname.split('/').slice(6),chunks=[];for await(const chunk of req)chunks.push(chunk);const body=chunks.length?JSON.parse(Buffer.concat(chunks)):{};
    if(path[0]==='signed-url'){
      if(body.if_none_match&&objects.has(body.key)){send(res,409,{});return true;}
      let receipt;if(body.method==='PUT'){receipt=randomUUID();receipts.set(receipt,{id:receipt,bucket_id:bucket,key:body.key,operation:'put',status:'pending',bytes:body.size_bytes,etag:''});}
      send(res,200,sign(body.key,body.method,{receipt,ifNoneMatch:body.if_none_match}));return true;
    }
    if(path[0]==='write-receipts'){send(res,receipts.has(path[1])?200:404,receipts.get(path[1])??{});return true;}
    if(path[0]!=='multipart-uploads'){send(res,404,{});return true;}
    if(path.length===1&&req.method==='POST'){
      let session=[...sessions.values()].find(s=>s.key===body.key&&!['completed','aborted'].includes(s.state));
      if(!session){session={id:randomUUID(),key:body.key,size_bytes:body.size_bytes,part_size_bytes:8,part_count:Math.ceil(body.size_bytes/8),content_type:body.content_type,state:'active',expires_at:'2099-01-01T00:00:00Z',created_at:'2026-01-01T00:00:00Z',parts:new Map()};sessions.set(session.id,session);}send(res,200,project(session));return true;
    }
    const session=sessions.get(path[1]);if(!session){send(res,404,{});return true;}
    if(path[2]==='parts'&&path.length===3){send(res,200,{items:[...session.parts].sort(([a],[b])=>a-b).map(([part_number,p])=>({part_number,etag:p.etag,size_bytes:p.data.length,last_modified:'2026-01-01T00:00:00Z'}))});return true;}
    if(path[2]==='parts'&&path[4]==='signed-url'){send(res,200,sign(session.key,'PUT',{part:Number(path[3]),upload:session.id}));return true;}
    if(path[2]==='complete'){
      if(body.parts.length!==session.part_count||body.parts.some((p,i)=>p.part_number!==i+1||p.etag!==session.parts.get(i+1)?.etag)){send(res,400,{});return true;}
      const data=Buffer.concat(body.parts.map(p=>session.parts.get(p.part_number).data));objects.set(session.key,data);session.state='completed';session.etag='"'+sha(data)+'"';
    }
    if(req.method==='DELETE')session.state='aborted';send(res,200,project(session));return true;
  }};
}

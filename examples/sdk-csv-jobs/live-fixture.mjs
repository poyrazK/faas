// Local protocol fixture, not deployed realtime acceptance. Uses Playwright's
// bundled WebSocket server so this example adds no application dependency.
import {randomBytes,randomUUID} from 'node:crypto';
import {wsServer} from '../../sdk/node/node_modules/playwright-core/lib/utilsBundle.js';
export function liveFixture(server){
  const endpointId='33333333333333333333333333333333',tickets=new Map(),channels=new Map(),clients=new Map(),keys=new Set();
  let issued=0,published=0;
  const sockets=new wsServer({noServer:true,handleProtocols:protocols=>protocols.has('gregale.realtime.v2')?'gregale.realtime.v2':false});
  const send=(socket,frame)=>{if(socket.readyState===1)socket.send(JSON.stringify(frame));};
  const token=request=>request.headers.authorization?.slice(7)??request.headers['sec-websocket-protocol']?.split(',').map(p=>p.trim()).find(p=>p.startsWith('gregale.realtime.bearer.'))?.slice('gregale.realtime.bearer.'.length);
  server.on('upgrade',(request,socket,head)=>{
    const ticket=tickets.get(token(request));
    if(request.url!=='/fixture-realtime'||!ticket||ticket.expires<=Date.now()){socket.end('HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n');return;}
    sockets.handleUpgrade(request,socket,head,connection=>{
      const state={ticket,channel:null};clients.set(connection,state);
      const expiry=setTimeout(()=>connection.close(1001),Math.max(1,ticket.expires-Date.now()));
      connection.on('close',()=>{clearTimeout(expiry);clients.delete(connection);});
      connection.on('message',bytes=>{
        const frame=JSON.parse(bytes.toString());
        if(frame.type==='read_state'&&state.channel===frame.channel){send(connection,{type:'read_receipt',channel:frame.channel,inbox:false,oldest_sequence:1,latest_sequence:(channels.get(frame.channel)??[]).length,sequence:0});return;}
        if(frame.type!=='subscribe')return;
        if(!ticket.channels.includes(frame.channel)){send(connection,{type:'error',channel:frame.channel,code:'not_authorized'});return;}
        state.channel=frame.channel;const log=channels.get(frame.channel)??[];
        send(connection,{type:'subscribed',channel:frame.channel,sequence:frame.after});
        for(const message of log)if(message.sequence>frame.after)send(connection,message);
      });
    });
  });
  return {
    config:()=>({endpointId,url:`ws://127.0.0.1:${server.address().port}/fixture-realtime`,pollDelayMs:30}),
    counts:()=>({issued,published,connections:clients.size}),
    expire(principal){for(const [socket,{ticket}]of clients){if(principal!==undefined&&ticket.principal!==principal)continue;tickets.delete(ticket.token);socket.close(1001);}},
    revoke(principal){for(const [key,ticket]of tickets)if(ticket.principal===principal)tickets.delete(key);for(const [socket,state]of clients)if(state.ticket.principal===principal){send(socket,{type:'error',channel:state.channel,code:'access_revoked'});}},
    async handle(request,response){
      if(!request.url.startsWith('/v1/apps/imports/realtime/endpoints/'+endpointId))return false;
      const sendHTTP=(status,value)=>{response.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});response.end(JSON.stringify(value));};
      if(request.headers.authorization!=='Bearer fixture-owner'){sendHTTP(403,{});return true;}
      const chunks=[];for await(const chunk of request)chunks.push(chunk);const body=JSON.parse(Buffer.concat(chunks).toString()||'{}');
      if(request.url.endsWith('/tickets')&&request.method==='POST'){
        const value={token:'grt_'+randomBytes(32).toString('base64url'),principal:body.principal,channels:body.channels,expires:Date.now()+body.ttl_seconds*1000};tickets.set(value.token,value);issued++;
        sendHTTP(201,{ticket:value.token,ticket_id:randomUUID(),expires_at:new Date(value.expires).toISOString()});return true;
      }
      const match=/\/channels\/([^/]+)\/publish/.exec(request.url);
      if(match){
        const channel=decodeURIComponent(match[1]),log=channels.get(channel)??[],key=request.headers['idempotency-key'];
        if(!keys.has(key)){
          keys.add(key);const message={type:'message',channel,sequence:log.length+1,message_id:randomUUID(),data_base64:body.data_base64,binary:false};log.push(message);channels.set(channel,log);published++;
          for(const [socket,state]of clients)if(state.channel===channel)send(socket,message);
        }sendHTTP(200,{sequence:log.length});return true;
      }
      sendHTTP(404,{});return true;
    },
    close(){for(const socket of clients.keys())socket.terminate();sockets.close();},
  };
}

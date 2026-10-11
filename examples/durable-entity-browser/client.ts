import {connectDurableEntityBrowserSession, type DurableEntityBrowserGrant} from '@gregale/sdk-node/durable-entities/browser';

// Your application backend authenticates this request, authorizes document:123,
// and mints a grant using its server-side Gregale API credential. The response
// also supplies the bound session UUID and generation (application metadata).
const response = await fetch('/api/document-session', {method: 'POST', credentials: 'same-origin'});
if (!response.ok) throw new Error('Document access denied');
const {grant, session_id, generation, api_origin}: {
  grant: DurableEntityBrowserGrant; session_id: string; generation: number; api_origin: string;
} = await response.json();
const socket = connectDurableEntityBrowserSession(api_origin, grant);
const open = {protocol_version: 1, request_id: `session/g${generation}/${session_id}/open`,
  payload: {kind: 'open', session_id, generation}};
socket.addEventListener('open', () => socket.send(JSON.stringify(open)));
socket.addEventListener('message', event => {
  const acknowledgement = JSON.parse(String(event.data));
  // Advance the outbox only after a matching 200 committed acknowledgement.
  // Persist command frames, never the grant. After a lost acknowledgement,
  // obtain a fresh grant for the same scope and resend the exact pending frame.
  console.log(acknowledgement);
});

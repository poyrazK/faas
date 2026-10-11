// Compile-only contract assertions; never invoke these calls at runtime.
import {counter, reservation, session, room, recoverableRoom} from '../client.mjs';
function checkTypes() {
  const count = counter({slug: 'app', key: 'customer:1'});
  const result: Promise<{value: {count: number}; version: number; replayed: boolean}> = count.invoke('one', {delta: 1});
  void result;
  recoverableRoom({slug:'app',key:'document:1'}).invoke('status',{kind:'status',session_id:'id',query_id:'query'});
  // @ts-expect-error lifecycle messages require their generation
  recoverableRoom({slug:'app',key:'document:1'}).invoke('one',{kind:'message',session_id:'id',sequence:1,payload:1});
  // @ts-expect-error lifecycle delta must be a number
  recoverableRoom({slug:'app',key:'document:1'}).invoke('one',{kind:'message',session_id:'id',generation:0,sequence:1,payload:'bad'});
  // @ts-expect-error room message delta is a number
  room({slug:'app',key:'document:1'}).invoke('one',{kind:'message',session_id:'id',sequence:1,payload:'bad'});
  // @ts-expect-error room messages require a payload
  room({slug:'app',key:'document:1'}).invoke('one',{kind:'message',session_id:'id',sequence:1});
  // @ts-expect-error delta must be a number
  count.invoke('one', {delta: '1'});
  // @ts-expect-error stable request identity is required
  count.invoke({delta: 1});
  // @ts-expect-error generated handle binds its namespace
  counter({slug: 'app', key: 'one', namespace: 'wrong'});
  // @ts-expect-error reservation action is literal
  reservation({slug: 'app', key: 'seat:1'}).invoke('one', {action: 'cancel', quantity: 1, webhookId: 'id'});
  // @ts-expect-error session requires a deadline
  session({slug: 'app', key: 'run:1'}).invoke('one', {action: 'start'});
}
void checkTypes;

// Compile-only contract assertions; never invoke these calls at runtime.
import {counter, reservation, session} from '../client.mjs';
function checkTypes() {
  const count = counter({slug: 'app', key: 'customer:1'});
  const result: Promise<{value: {count: number}; version: number; replayed: boolean}> = count.invoke('one', {delta: 1});
  void result;
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

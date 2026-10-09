// ADR-715: kill after business commit and durable outbox, before first publication.
import pg from '../../../examples/customer-operation-orders/node_modules/pg/lib/index.js';
import {createOrderServer} from '../../../examples/customer-operation-orders/server.mjs';
import {GregaleOperations} from '../../../sdk/node/dist/index.js';

const config = JSON.parse(process.env.GREGALE_ORDERS_TEST_CONFIG);
const pool = new pg.Pool({connectionString: config.database});
const runtime = new GregaleOperations({apiURL: config.api, identityEndpoint: config.identity});
const transact = runtime.transaction.bind(runtime);
let callbacks = 0, replayed = false;
runtime.transaction = async (request, pool, handler) => {
  let receipt;
  try {receipt = await transact(request, pool, async tx => { callbacks++; return handler(tx); });}
  catch (error) {process.stderr.write(JSON.stringify({error: error.name, code: error.code, message: error.message})+'\n');throw error;}
  replayed = receipt.replayed;
  return receipt;
};
if (config.loseReply) runtime.milestone = async () => {
  const saved = await pool.query('SELECT response_body FROM public.gregale_customer_operation_inbox WHERE operation_id=$1', [runtime.context().id]);
  process.stdout.write(JSON.stringify({body: saved.rows[0].response_body, replayed: false, callbacks, status: 200}) + '\n');
  await new Promise(() => {}); // Go observes committed SQL then kills before any publication.
};
const server = createOrderServer({runtime, pool});
server.prependListener('request', (req, res) => {
  const end = res.end.bind(res);
  res.end = (body, ...args) => {
    if (req.url === '/orders/fulfill') {
      process.stdout.write(JSON.stringify({body, replayed, callbacks, status: res.statusCode}) + '\n');
      if (config.loseReply && res.statusCode === 200) return res; // Go kills this process after observing the committed response.
    }
    return end(body, ...args);
  };
});
server.listen(0, '127.0.0.1', () => process.stdout.write('127.0.0.1:' + server.address().port + '\n'));

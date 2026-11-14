// Standard grpc-js success requires the final grpc-status, not just HTTP 200.
const grpc = require('@grpc/grpc-js');
const [host, ip] = process.argv.slice(2);
if (!host) throw new Error('usage: node client.cjs HOST [DIRECT_IP]');
const Client = grpc.makeGenericClientConstructor(require('./service.cjs'), 'Echo');
const client = new Client(`${ip || host}:443`, grpc.credentials.createSsl(),
  ip ? {'grpc.ssl_target_name_override': host, 'grpc.default_authority': host} : {});
const deadline = () => ({deadline: new Date(Date.now() + 30000)});
(async () => {
  let failed = false;
  for (const method of ['Say', 'Stream', 'Fail']) {
    await new Promise(resolve => {
      const messages = [];
      const call = method === 'Stream' ? client.Stream(Buffer.from('hi'), deadline()) :
        client[method](Buffer.from('hi'), deadline(), (err, msg) => { if (msg) messages.push(msg.toString()); });
      if (method === 'Stream') { call.on('data', msg => messages.push(msg.toString())); call.on('error', () => {}); }
      call.on('status', status => {
        const expected = method === 'Fail' ? grpc.status.FAILED_PRECONDITION : grpc.status.OK;
        const expectedMessages = method === 'Say' ? ['echo:hi'] : method === 'Stream' ? ['one', 'two', 'three'] : [];
        const ok = status.code === expected && JSON.stringify(messages) === JSON.stringify(expectedMessages);
        console.log(JSON.stringify({method, code: status.code, details: status.details, messages, ok}));
        failed ||= !ok; resolve();
      });
    });
  }
  client.close(); process.exitCode = failed ? 1 : 0;
})().catch(err => { console.error(err.message); client.close(); process.exitCode = 1; });

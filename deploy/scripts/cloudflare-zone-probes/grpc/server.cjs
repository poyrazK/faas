const grpc = require('@grpc/grpc-js');
const service = require('./service.cjs');
const server = new grpc.Server();
server.addService(service, {
  Say: (call, cb) => cb(null, Buffer.concat([Buffer.from('echo:'), call.request])),
  Stream: call => { for (const value of ['one', 'two', 'three']) call.write(Buffer.from(value)); call.end(); },
  Fail: (_call, cb) => cb({code: grpc.status.FAILED_PRECONDITION, details: 'zone deliberate failure'}),
  Check: (_call, cb) => cb(null, Buffer.from([8, 1])),
});
server.bindAsync(`0.0.0.0:${process.env.PORT || 3000}`, grpc.ServerCredentials.createInsecure(), err => {
  if (err) throw err;
  console.log('zone gRPC probe ready');
});

const raw = b => b;
module.exports = Object.fromEntries(['Say', 'Stream', 'Fail', 'Check'].map(name => [name, {
  path: name === 'Check' ? '/grpc.health.v1.Health/Check' : `/zone.Echo/${name}`,
  requestStream: false, responseStream: name === 'Stream',
  requestSerialize: raw, requestDeserialize: raw, responseSerialize: raw, responseDeserialize: raw,
}]));

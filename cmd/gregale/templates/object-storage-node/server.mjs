import {createStorageApp} from './app.mjs';

let app;
let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  if (app && !await app.stop()) process.exitCode = 1;
}
process.once('SIGTERM', () => {void stop();});
process.once('SIGINT', () => {void stop();});
try {
  app = createStorageApp({log: code => console.error(JSON.stringify({code}))});
  const address = await app.start();
  console.log(JSON.stringify({event: 'listening', port: address.port}));
} catch {
  if (!stopping) {console.error(JSON.stringify({code: 'startup_unavailable'})); process.exitCode = 1;}
  await stop();
}

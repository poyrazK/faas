import net from "node:net";
import tls from "node:tls";
import { createConnectionStalls } from "./pg-connection-stall.mjs";
import { createReadCuts } from "./pg-read-cut.mjs";
import { createBandwidthLimiter } from "./pg-bandwidth.mjs";
import { createFaultClock, faultPlan } from "./pg-fault-clock.mjs";
import { relayFrames } from "./pg-frame-relay.mjs";
import { acceptPostgresTLS, connectPostgresTLS } from "./pg-tls-transport.mjs";

// Opt-in loopback protocol fixture with optional verified TLS on both legs. Never logs or retains SQL/auth payloads.
// One explicitly armed COMMIT fault; drop, delay or stall with bounded timing.
export async function createCommitProxy(connectionString, { tls: encryption, allowRemoteTarget = false } = {}) {
  const target = new URL(connectionString);
  if (!["postgres:", "postgresql:"].includes(target.protocol) || (target.hostname !== "127.0.0.1" && !(allowRemoteTarget === true && encryption))) throw new Error("commit_proxy_requires_loopback_fixture");
  const mode = target.searchParams.get("sslmode");
  if (encryption ? !["require", "verify-full"].includes(mode) : mode !== "disable") throw new Error("commit_proxy_requires_explicit_transport");
  if (encryption && (!encryption.key || !encryption.cert || !encryption.ca || !encryption.servername)) throw new Error("commit_proxy_requires_tls_identity_and_trust");
  const context = encryption ? tls.createSecureContext({ key: encryption.key, cert: encryption.cert, minVersion: "TLSv1.2" }) : undefined;
  const sockets = new Set();
  const evidence = { commits_forwarded: 0, acknowledgements_dropped: 0, commit_errors_forwarded: 0, invalid_frames: 0, tls_connections_verified: 0, tls_failures: 0, connection_attempts: 0, ssl_requests_seen: 0, authentication_challenges_seen: 0 };
  let armed = null;
  let armCount = 0;
  const clock = createFaultClock();
  const bandwidth = createBandwidthLimiter();
  const readCuts = createReadCuts();
  const connectionStalls = createConnectionStalls(Boolean(encryption));
  let closing;
  let stopped = false;
  const connections = new Set();
  const negotiations = new Set();
  const relays = new Set();
  const authenticating = new Set();
  const server = net.createServer(rawClient => {
    evidence.connection_attempts++;
    const owned = new Set();
    connections.add(owned);
    const holds = new Set();
    const destroy = () => { for (const hold of holds) hold.cancel(); for (const socket of owned) socket.destroy(); };
    const track = socket => {
      owned.add(socket); sockets.add(socket);
      socket.setNoDelay(true);
      socket.on("error", destroy);
      socket.on("close", () => { sockets.delete(socket); owned.delete(socket); destroy(); if (!owned.size) connections.delete(owned); });
      socket.setTimeout(10000, destroy);
      if (stopped) socket.destroy();
    };
    rawClient.once("close", () => authenticating.delete(rawClient));
    track(rawClient);
    async function connect() {
      let client = rawClient;
      let upstream;
      if (encryption) {
        client = await acceptPostgresTLS(rawClient, context, track, socket => { evidence.ssl_requests_seen++; return connectionStalls.wait(socket); });
        if (stopped || client.destroyed) throw new Error("commit_proxy_closed");
        client.pause();
        upstream = await connectPostgresTLS(target, encryption, track);
        if (stopped || client.destroyed || upstream.destroyed) throw new Error("commit_proxy_closed");
        evidence.tls_connections_verified++;
        authenticating.add(rawClient);
      } else {
        upstream = net.connect(Number(target.port || 5432), target.hostname);
        track(upstream);
      }
      let selected;
      let challengeSeen = false;
      let completed = [];
      let completedBytes = 0;
      const invalid = () => { evidence.invalid_frames++; destroy(); };
      const hold = async (plan, messages, destination) => {
        const pending = clock.begin(plan);
        holds.add(pending);
        const outcome = await pending.done;
        holds.delete(pending);
        if (outcome === "released") {
          for (const message of messages) await bandwidth.write(plan.direction, destination, message);
        } else if (outcome !== "canceled") {
          if (plan.direction === "response") evidence.acknowledgements_dropped++;
          destroy();
        }
        return outcome;
      };
      const relay = (source, options) => {
        const work = relayFrames(source, options);
        relays.add(work);
        source.once("close", () => work.join().then(() => relays.delete(work)));
      };
      relay(client, { startup: true, invalid, frame: async (message, initial) => {
        const commit = !initial && message[0] === 81 && message.subarray(5, -1).toString("utf8").trim().toUpperCase() === "COMMIT";
        if (commit && armed) { selected = armed; armed = null; }
        if (commit && selected?.direction === "request") {
          const plan = selected;
          selected = null;
          if (await hold(plan, [message], upstream) === "released") evidence.commits_forwarded++;
          return;
        }
        if (commit) evidence.commits_forwarded++;
        await bandwidth.write("request", upstream, message);
      } });
      relay(upstream, { invalid, frame: async message => {
        if (message[0] === 82 && message.length < 9) { invalid(); return; }
        const authCode = message[0] === 82 ? message.readInt32BE(5) : undefined;
        if ([3, 5, 10].includes(authCode) && (authCode === 3 && message.length !== 9 || authCode === 5 && message.length !== 13 ||
            authCode === 10 && (message.length < 12 || message[message.length - 1] !== 0 || message[message.length - 2] !== 0))) { invalid(); return; }
        if (!challengeSeen && [3, 5, 10].includes(authCode)) {
          challengeSeen = true; evidence.authentication_challenges_seen++;
          const pending = connectionStalls.wait(client, "authentication");
          if (pending && !await pending) { destroy(); return; }
        }
        if (message[0] === 90) authenticating.delete(rawClient);
        if (selected?.direction === "response") {
          if (message[0] === 69) {
            if (completed.length) { invalid(); return; }
            selected = null;
            evidence.commit_errors_forwarded++;
          } else if (message[0] === 67 && message.subarray(5).equals(Buffer.from("COMMIT\0"))) {
            completed.push(message); completedBytes += message.length;
            return;
          } else if (completed.length) {
            completed.push(message); completedBytes += message.length;
            if (completedBytes > 1024 * 1024) { invalid(); return; }
            if (message[0] === 90) {
              if (message.length !== 6 || message[5] !== 73) { invalid(); return; }
              const plan = selected;
              selected = null;
              const messages = completed;
              completed = []; completedBytes = 0;
              await hold(plan, messages, client);
            }
            return;
          } else if (message[0] === 90) selected = null;
        }
        if (!await readCuts.write(message, client, prefix => bandwidth.write("response", client, prefix), destroy)) await bandwidth.write("response", client, message);
      } });
      client.resume();
    }
    const negotiation = connect().catch(() => { if (encryption && !stopped) evidence.tls_failures++; destroy(); });
    negotiations.add(negotiation);
    negotiation.finally(() => negotiations.delete(negotiation));
  });
  server.maxConnections = 16;
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const proxied = new URL(connectionString);
  proxied.hostname = "127.0.0.1";
  proxied.port = String(server.address().port);
  return {
    connectionString: proxied.href,
    arm(plan) {
      if (stopped) throw new Error("commit_proxy_closed");
      if (armed) throw new Error("commit_proxy_already_armed");
      if (armCount >= 100) throw new Error("commit_proxy_fault_limit");
      armed = faultPlan(plan); armCount++;
    },
    stallConnections(plan) { connectionStalls.enable(plan); },
    clearConnectionStall() { connectionStalls.clear(); },
    armReadCut(plan) { readCuts.arm(plan); },
    clearReadCut() { readCuts.clear(); },
    limitBandwidth(plan) { bandwidth.limit(plan); },
    clearBandwidth(direction) { bandwidth.clear(direction); },
    evidence() { return { ...evidence, ...clock.evidence(), ...bandwidth.evidence(), ...readCuts.evidence(), ...connectionStalls.evidence(), pending_connections: negotiations.size, pending_authentication: authenticating.size, armed: Boolean(armed), connections: connections.size }; },
    close() {
      if (!closing) {
        stopped = true;
        clock.cancel();
        connectionStalls.close();
        readCuts.close();
        const transfers = bandwidth.close();
        const joined = [...sockets].map(socket => new Promise(resolve => socket.once("close", resolve)));
        for (const socket of sockets) socket.destroy();
        joined.push(new Promise(resolve => server.close(resolve)));
        closing = Promise.all([transfers, ...joined, ...negotiations, ...[...relays].map(relay => relay.join())]).then(() => undefined);
      }
      return closing;
    },
  };
}

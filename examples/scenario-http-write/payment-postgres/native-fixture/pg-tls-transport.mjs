import net from "node:net";
import tls from "node:tls";

const sslRequest = Buffer.from([0, 0, 0, 8, 4, 210, 22, 47]);

// PostgreSQL negotiates TLS before sending its startup packet. Never fall back.
export function acceptPostgresTLS(socket, context, track, beforeSSLReply) {
  return new Promise((resolve, reject) => {
    let buffered = Buffer.alloc(0);
    const fail = () => { clearTimeout(deadline); reject(new Error("postgres_tls_negotiation_failed")); };
    let deadline = setTimeout(() => { socket.destroy(); fail(); }, 2000);
    socket.once("close", fail);
    const receive = async chunk => {
      buffered = Buffer.concat([buffered, chunk]);
      if (buffered.length < 8) return;
      socket.removeListener("data", receive);
      if (!buffered.equals(sslRequest)) { socket.destroy(); fail(); return; }
      socket.pause();
      try {
        const pending = beforeSSLReply?.(socket);
        if (pending) {
          clearTimeout(deadline);
          if (!await pending) { socket.destroy(); fail(); return; }
          deadline = setTimeout(() => { socket.destroy(); fail(); }, 2000);
        }
      } catch { socket.destroy(); fail(); return; }
      if (socket.destroyed) { fail(); return; }
      socket.removeListener("close", fail);
      socket.write("S");
      const encrypted = new tls.TLSSocket(socket, { isServer: true, secureContext: context });
      track(encrypted);
      encrypted.once("close", fail);
      encrypted.once("secure", () => { encrypted.removeListener("close", fail); clearTimeout(deadline); resolve(encrypted); });
      socket.resume();
    };
    socket.on("data", receive);
  });
}

export function connectPostgresTLS(target, options, track) {
  return new Promise((resolve, reject) => {
    const socket = net.connect(Number(target.port || 5432), target.hostname);
    track(socket);
    const fail = () => { clearTimeout(deadline); reject(new Error("postgres_tls_verification_failed")); };
    const deadline = setTimeout(() => { socket.destroy(); fail(); }, 2000);
    socket.once("close", fail);
    socket.once("connect", () => socket.write(sslRequest));
    socket.once("data", reply => {
      socket.removeListener("close", fail);
      if (reply.length !== 1 || reply[0] !== 83) { socket.destroy(); fail(); return; }
      const encrypted = tls.connect({ socket, ca: options.ca, servername: options.servername,
        rejectUnauthorized: true, minVersion: "TLSv1.2" });
      track(encrypted);
      encrypted.once("close", fail);
      encrypted.once("secureConnect", () => {
        encrypted.removeListener("close", fail);
        clearTimeout(deadline);
        resolve(encrypted);
      });
    });
  });
}

import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { join } from "node:path";
/** Private NDJSON pipe: imports once; each call creates fresh clients and connections. */
export async function pythonFixture({ python, home, env, children, cleanup }) {
  const worker = spawn(python, [join(home, "app/event_driver.py"), "--stdio"], {
    env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  children.add(worker);
  let seq = 0,
    errors = "",
    ready;
  const pending = new Map(),
    lines = createInterface({ input: worker.stdout });
  worker.stderr.on("data", (b) => (errors += b));
  const started = new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(Error("Python fixture startup timed out")),
      120000,
    );
    ready = () => {
      clearTimeout(timer);
      resolve();
    };
    worker.once("exit", (code) => {
      clearTimeout(timer);
      reject(Error("Python fixture stopped " + code + " " + errors));
    });
  });
  lines.on("line", (line) => {
    const packet = JSON.parse(line);
    if (packet.ready) {
      ready();
      return;
    }
    pending.get(packet.request_id)?.(packet.result);
    pending.delete(packet.request_id);
  });
  worker.once("exit", () => children.delete(worker));
  cleanup.push(() => lines.close());
  await started;
  return (request) =>
    new Promise((resolve, reject) => {
      const request_id = String(seq++),
        timer = setTimeout(() => {
          pending.delete(request_id);
          reject(Error("Python fixture request timed out " + errors));
        }, 30000);
      pending.set(request_id, (result) => {
        clearTimeout(timer);
        resolve(result);
      });
      worker.stdin.write(JSON.stringify({ ...request, request_id }) + "\n");
    });
}

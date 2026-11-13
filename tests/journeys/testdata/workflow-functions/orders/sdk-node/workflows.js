// A minimal code-workflow protocol core (call and waitForEvent), vendored so
// the journey does not depend on a published SDK.
export function defineWorkflow(name, run) { return { name, run }; }
export function serveWorkflows(defs) {
  return async (request) => {
    if (request.headers.get("x-faas-workflow-mode") !== "code" || !request.headers.get("x-faas-workflow-run-id")) {
      return Response.json({ error: "workflow entry handlers only accept Gregale workflow calls" }, { status: 403 });
    }
    const req = await request.json();
    const def = defs.find((d) => d.name === req.workflow);
    if (!def) return Response.json({ error: "unknown workflow" }, { status: 404 });
    const ops = [];
    let seq = 0;
    const SUSPEND = Symbol("suspend");
    const claim = () => { const s = seq++; return { s, e: req.journal[s] }; };
    const wf = {
      async call(fn, input) {
        const { s, e } = claim();
        if (e && e.status === "succeeded") return e.output;
        if (e && e.status === "failed") throw new Error(e.error);
        if (!e) ops.push({ seq: s, name: fn, kind: "call", function: fn, input });
        throw SUSPEND;
      },
      async waitForEvent(name, o) {
        const event = (o && o.event) || name;
        const { s, e } = claim();
        if (e && e.status === "succeeded") return e.timed_out ? null : (e.output ?? null);
        if (!e) ops.push({ seq: s, name, kind: "event", event });
        throw SUSPEND;
      },
    };
    let result = { status: "running" };
    try {
      result = { status: "succeeded", output: await def.run(req.input, wf) };
    } catch (err) {
      if (err !== SUSPEND) result = { status: "failed", error: String(err && err.message || err) };
    }
    return Response.json({ protocol: req.protocol, ops, result, replayed_ops: Math.min(seq, req.journal.length) });
  };
}

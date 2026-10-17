function requestSignal(req, res) {
  const controller = new AbortController();
  const abort = () => controller.abort();
  const close = () => { if (!res.writableFinished) abort(); };
  req.once("aborted", abort); res.once("close", close);
  if (req.aborted) abort();
  return { signal: controller.signal, dispose() {
    req.off("aborted", abort); res.off("close", close);
  } };
}

function failure(res, error) {
  const code = ["database_busy", "database_cancelled"].includes(error?.code) ? error.code : "database_unavailable";
  res.status(503).json({ ok: false, error: code });
}

// Lifecycle callbacks come from guest-init over loopback with one exact marker
// header; anything else is a forged public request.
function lifecycleHook(app, path, marker, run) {
  app.post(path, async (req, res) => {
    if (!["127.0.0.1", "::1", "::ffff:127.0.0.1"].includes(req.socket.remoteAddress) ||
        req.get(marker) !== "1") return res.sendStatus(403);
    const request = requestSignal(req, res);
    try { await run(request); res.sendStatus(204); }
    catch (error) { failure(res, error); }
    finally { request.dispose(); }
  });
}

export function createApp(express, database) {
  const app = express();
  app.use(express.json({ limit: "64kb" }));
  app.get("/notes", async (req, res) => {
    const request = requestSignal(req, res);
    try {
      const { rows } = await database.query("SELECT id, body, created_at FROM notes ORDER BY id DESC LIMIT 50", [], request);
      res.json({ ok: true, notes: rows });
    } catch (error) { failure(res, error); }
    finally { request.dispose(); }
  });
  app.post("/notes", async (req, res) => {
    const body = req.body?.body;
    if (typeof body !== "string" || !body) return res.status(400).json({ ok: false, error: "missing_body" });
    const request = requestSignal(req, res);
    try {
      const { rows } = await database.query("INSERT INTO notes (body) VALUES ($1) RETURNING id, body, created_at", [body], { ...request, write: true });
      res.status(201).json({ ok: true, note: rows[0] });
    } catch (error) { failure(res, error); }
    finally { request.dispose(); }
  });
  app.get("/healthz", async (req, res) => {
    const request = requestSignal(req, res);
    try {
      await database.query("SELECT id FROM notes LIMIT 1", [], request);
      res.json({ ok: true, db: "ok" });
    } catch (error) { failure(res, error); }
    finally { request.dispose(); }
  });
  // Drain every SQL session before a capture; replace any pool after restore.
  lifecycleHook(app, "/internal/before-checkpoint", "X-Faas-Before-Checkpoint", request => database.checkpoint(request));
  lifecycleHook(app, "/internal/after-restore", "X-Faas-After-Restore", request => database.restore(request));
  return app;
}

"""HTTP routes; Uvicorn must run with proxy headers disabled."""

import asyncio
import json
from contextlib import asynccontextmanager

from database import READINESS_SQL, Database, DatabaseError
from fastapi import FastAPI, HTTPException, Request, Response


async def run_request(request, operation):
    async def disconnected():
        while (await request.receive())["type"] != "http.disconnect":
            pass

    task = asyncio.create_task(operation)
    watcher = asyncio.create_task(disconnected())
    try:
        await asyncio.wait((task, watcher), return_when=asyncio.FIRST_COMPLETED)
        if watcher.done() and not task.done():
            raise DatabaseError("database_cancelled")
        return task.result()
    except DatabaseError:
        raise HTTPException(503, "Database temporarily unavailable") from None
    finally:
        watcher.cancel()
        if not task.done():
            task.cancel()
        await asyncio.gather(task, watcher, return_exceptions=True)


async def note_body(request):
    raw = bytearray()
    async for chunk in request.stream():
        if len(raw) + len(chunk) > 65536:
            raise HTTPException(413, "Request too large")
        raw.extend(chunk)
    try:
        parsed = json.loads(raw)
        body = parsed.get("body") if isinstance(parsed, dict) else None
        if not isinstance(body, str) or not body:
            raise ValueError
        return body
    except (ValueError, UnicodeError):
        raise HTTPException(400, "A nonempty body string is required") from None


def create_app(database_factory=Database):
    @asynccontextmanager
    async def lifespan(application):
        database = database_factory()
        application.state.database = database
        try:
            await database.start()
            yield
        finally:
            await database.close()

    app = FastAPI(lifespan=lifespan)

    @app.get("/notes")
    async def list_notes(request: Request):
        rows = await run_request(request, app.state.database.query(
            "SELECT id, body, created_at FROM public.notes ORDER BY id DESC LIMIT 50"))
        return {"ok": True, "notes": rows}

    @app.post("/notes", status_code=201)
    async def create_note(request: Request):
        body = await note_body(request)
        rows = await run_request(request, app.state.database.query(
            "INSERT INTO public.notes (body) VALUES (%s) RETURNING id, body, created_at", (body,), write=True))
        return {"ok": True, "note": rows[0]}

    @app.get("/healthz")
    async def health(request: Request):
        await run_request(request, app.state.database.query(READINESS_SQL))
        return {"ok": True, "db": "ok"}

    @app.post("/internal/after-restore", status_code=204)
    async def after_restore(request: Request):
        if (request.client is None or request.client.host not in {"127.0.0.1", "::1", "::ffff:127.0.0.1"}
                or request.headers.get("X-Faas-After-Restore") != "1"):
            raise HTTPException(403, "Forbidden")
        await run_request(request, app.state.database.restore())
        return Response(status_code=204)

    return app

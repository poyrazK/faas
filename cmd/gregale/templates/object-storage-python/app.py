"""Authenticated storage routes; actual peer addresses authenticate restore hooks."""

import asyncio
import re
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request, Response
from storage import Storage, StorageError, TransferResponse


def create_app(storage_factory=Storage):
    @asynccontextmanager
    async def lifespan(application):
        application.state.storage = service = storage_factory()
        try:
            await service.start()
            yield
        finally:
            await service.close()

    app = FastAPI(lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)

    @app.get("/healthz")
    async def health():
        return {"ok": True}

    @app.get("/readyz")
    async def ready():
        if not app.state.storage.ready():
            raise HTTPException(503, "Storage temporarily unavailable")
        return {"ok": True}

    @app.api_route("/objects/{key:path}", methods=["GET", "PUT"])
    async def transfer(request: Request, key: str):
        try:
            app.state.storage.authorize(request.headers.get("authorization", ""))
        except StorageError as error:
            raise HTTPException(error.status, error.code) from None
        if request.url.query or re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", key) is None:
            raise HTTPException(400, "Invalid object key")
        return TransferResponse(app.state.storage, request, key)

    @app.post("/internal/after-restore", status_code=204)
    async def after_restore(request: Request):
        if (request.client is None or request.client.host not in {"127.0.0.1", "::1", "::ffff:127.0.0.1"}
                or request.headers.get("X-Faas-After-Restore") != "1"):
            raise HTTPException(403, "Forbidden")
        async def disconnected():
            while (await request.receive())["type"] != "http.disconnect":
                pass

        task = asyncio.create_task(app.state.storage.restore())
        watcher = asyncio.create_task(disconnected())
        try:
            await asyncio.wait((task, watcher), return_when=asyncio.FIRST_COMPLETED)
            if not task.done():
                raise HTTPException(503, "Storage temporarily unavailable")
            task.result()
        except StorageError:
            raise HTTPException(503, "Storage temporarily unavailable") from None
        finally:
            watcher.cancel()
            if not task.done():
                task.cancel()
            await asyncio.gather(task, watcher, return_exceptions=True)
        return Response(status_code=204)

    return app

"""Run one main-thread event loop with: python app.py."""

import asyncio
import logging
from contextlib import asynccontextmanager

from faas_sdk.async_secret_reload_runtime import AsyncSecretReloadCoordinator
from fastapi import FastAPI, HTTPException
from storage import StorageService, make_binding

logger = logging.getLogger(__name__)


def create_app(*, env=None, io=None, binding_factory=None, extra_bindings=(), readiness_key="readiness.txt"):
    @asynccontextmanager
    async def lifespan(application):
        storage = binding_factory() if binding_factory else make_binding(readiness_key=readiness_key)
        service = StorageService(storage)
        bindings = (*extra_bindings, storage)
        coordinator = AsyncSecretReloadCoordinator([binding.secret_consumer() for binding in bindings], env=env, io=io,
                                                    on_error=lambda _error: logger.warning("secret_adoption_failed"))
        application.state.service, application.state.coordinator = service, coordinator
        try:
            await coordinator.start()
            yield
        finally:
            service.stop()
            coordinator.stop()
            try:
                async with asyncio.timeout(5):
                    await coordinator.wait_stopped()
                    await service.wait_closed()
                    for binding in bindings:
                        await binding.wait_for_retirement()
            except Exception:
                logger.warning("storage_cleanup_incomplete")

    application = FastAPI(lifespan=lifespan)

    @application.get("/objects/{key:path}")
    async def download(key: str):
        return application.state.service.download(key)

    @application.get("/healthz")
    async def health():
        try:
            async with asyncio.timeout(3):
                async with application.state.service.binding.acquire() as lease:
                    owned, bucket = lease.current()
                    await owned.client.head_object(Bucket=bucket, Key=readiness_key)
            return {"ok": True}
        except Exception:
            raise HTTPException(503, "Storage temporarily unavailable") from None

    return application


app = create_app()

if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8080, workers=1, proxy_headers=False)

import logging
import os

import uvicorn
from app import create_app


class StorageServer(uvicorn.Server):
    def __init__(self, application, **options):
        self.application = application
        super().__init__(uvicorn.Config(application, **options))

    def handle_exit(self, sig, frame):
        service = getattr(self.application.state, "storage", None)
        if service is not None:
            service.stop()
        super().handle_exit(sig, frame)


if __name__ == "__main__":
    try:
        # One main-thread loop owns SIGHUP/clients. Forwarded headers must never
        # replace the actual peer address used to authenticate the restore hook.
        StorageServer(create_app(), host="0.0.0.0", port=int(os.environ.get("PORT", "8080")),
                      workers=1, proxy_headers=False, timeout_graceful_shutdown=5, log_level="warning").run()
    except Exception:
        logging.getLogger(__name__).error("application_startup_failed")
        raise SystemExit(1) from None

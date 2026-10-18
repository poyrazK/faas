import logging
import os

import uvicorn
from app import create_app

if __name__ == "__main__":
    # One main-thread event loop owns SIGHUP and all pools. Peer authentication
    # for the restore hook must never trust forwarded client-address headers.
    try:
        uvicorn.run(create_app(), host="0.0.0.0", port=int(os.environ.get("PORT", "8080")),
                    workers=1, proxy_headers=False, timeout_graceful_shutdown=5, log_level="warning")
    except Exception:
        logging.getLogger(__name__).error("application_startup_failed")
        raise SystemExit(1) from None

"""Native Job entry using the shared approved-plan and atomic SQL effect protocol."""

import asyncio
import json
import os

from compensation_backend import backend
from faas_sdk import run_job_task


async def main():
    async with backend(
        os.environ.get("GREGALE_COMP_REVIEW_URL", "http://127.0.0.1/")
    ) as (recovery, handler):
        from faas_sdk.preview import FaaSClient, FileOptions

        async with FaaSClient(
            base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"]
        ) as client:
            store = await client.files(
                app_slug="imports", bucket=os.environ["GREGALE_FILE_BUCKET"]
            ).job_store(options=FileOptions(timeout=10))
            await run_job_task(
                recovery.worker_contract, handler, timeout=60, store=store
            )


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except Exception as error:  # noqa: BLE001 -- redact failures at the process boundary.
        print(json.dumps({"code": getattr(error, "code", "handler_failed")}))
        raise SystemExit(1) from None

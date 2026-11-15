"""The same contract/manifest protocol, executed by a Python task image."""

import asyncio
import importlib.util
import json
import os
import re

import psycopg
from faas_sdk import JobContext, LocalJobStore, run_job_task
from generated import Input, Output, contract


async def handler(input: Input, context: JobContext) -> Output:
    context.check_cancelled()
    async with await psycopg.AsyncConnection.connect(
        os.environ["DATABASE_URL"]
    ) as conn:
        if not re.fullmatch(r"[^@\s]+@[^@\s]+", input["email"]):
            return {"row": input["row"], "saved": False, "rejection": "invalid_email"}
        cursor = await conn.execute(
            "INSERT INTO sdk_csv_attempts VALUES (%s, 1) ON CONFLICT (input_id) DO UPDATE SET calls=sdk_csv_attempts.calls+1 RETURNING calls",
            (context.input_id,),
        )
        marker = await cursor.fetchone()
        assert marker is not None
        count = marker[0]
        await conn.commit()
        if input.get("fail_once") and count == 1:
            raise RuntimeError("Injected transient import failure")
        context.check_cancelled()
        await conn.execute(
            "INSERT INTO sdk_csv_rows VALUES (%s,%s,%s) ON CONFLICT (input_id) DO NOTHING",
            (context.input_id, input["row"], input["email"]),
        )
    return {"row": input["row"], "saved": True, "rejection": "none"}


async def main():
    file_client = None
    if "GREGALE_FILE_BUCKET" in os.environ:
        from faas_sdk.preview import FaaSClient, FileOptions

        file_client = FaaSClient(
            base_url=os.environ["FAAS_API"],
            token=os.environ.get("GREGALE_FILE_TOKEN", os.environ.get("FAAS_TOKEN")),
        )
        store = await file_client.files(
            app_slug=os.environ["GREGALE_FILE_APP"],
            bucket=os.environ["GREGALE_FILE_BUCKET"],
        ).job_store(options=FileOptions(timeout=60))
    elif "GREGALE_LOCAL_JOB_STORE" in os.environ:
        store = LocalJobStore(os.environ["GREGALE_LOCAL_JOB_STORE"])
    else:
        spec = importlib.util.spec_from_file_location(
            "application_job_store", os.environ["GREGALE_JOB_STORE_MODULE"]
        )
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        store = module.store
    try:
        await run_job_task(contract, handler, timeout=60, store=store)
    except Exception as error:  # noqa: BLE001 -- redact adapter/database failures at the process boundary.
        print(
            json.dumps({"code": getattr(error, "code", "handler_failed")}), flush=True
        )
        raise SystemExit(1) from None
    finally:
        if file_client is not None:
            await file_client.aclose()


if __name__ == "__main__":
    asyncio.run(main())

"""A native task image entry for validation, import, or report generation."""

import asyncio
import csv
import io
import json
import os
import re
from pathlib import Path

from faas_sdk import JobContract, run_job_task
from faas_sdk.job_runtime import job_hash
from faas_sdk.preview import FaaSClient, FileOptions, read_pipeline_artifact
from worker import handler as import_handler


async def main():
    stage = os.environ["GREGALE_CSV_PIPELINE_STAGE"]
    if stage not in ("validate", "import", "report"):
        raise ValueError("Invalid pipeline stage")
    path = Path(__file__).parent / (
        "contract.json" if stage == "import" else stage + "-contract.json"
    )
    contract = JobContract(json.loads(path.read_text()))
    async with FaaSClient(
        base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"]
    ) as client:
        files = client.files(
            app_slug="imports", bucket=os.environ["GREGALE_FILE_BUCKET"]
        )
        store = await files.job_store(options=FileOptions(timeout=10))

        async def handle(value, context):
            context.check_cancelled()
            if stage == "import":
                return await import_handler(value, context)
            if not re.fullmatch(r"[a-f0-9-]{36}", value["import_id"]):
                raise ValueError("Invalid import identity")
            if stage == "validate":
                raw = await read_pipeline_artifact(
                    files,
                    value["source"],
                    value["source"]["owner_id"],
                    options=FileOptions(timeout=10, max_bytes=1024 * 1024),
                )
                rows = list(
                    csv.reader(
                        io.StringIO(raw.decode("utf-8"), newline=""), strict=True
                    )
                )
                if (
                    not rows
                    or rows.pop(0) != ["row", "email"]
                    or not 1 <= len(rows) <= 1000
                    or any(
                        len(row) != 2 or not re.fullmatch(r"[1-9][0-9]*", row[0])
                        for row in rows
                    )
                ):
                    raise ValueError("Invalid CSV")
                return {
                    "rows": [
                        {
                            "id": value["import_id"] + ":" + str(n + 1),
                            "value": {"row": int(row[0]), "email": row[1]},
                        }
                        for n, row in enumerate(rows)
                    ]
                }
            if os.environ.get("GREGALE_CSV_REPORT_FAIL") == "1":
                raise RuntimeError("Injected report failure after import")
            key = "pipelines/" + value["import_id"] + "/rejections.csv"
            raw = (
                "input_id,reason\n"
                + "\n".join(i + ",invalid_email" for i in value["rejections"])
                + "\n"
            ).encode()
            try:
                actual = await files.download(
                    key, options=FileOptions(timeout=10, max_bytes=1024 * 1024)
                )
            except Exception as error:
                if getattr(error, "status", None) != 404:
                    raise
                await files.put(
                    key,
                    raw,
                    if_none_match="*",
                    content_type="text/csv",
                    options=FileOptions(timeout=10),
                )
                actual = await files.download(
                    key, options=FileOptions(timeout=10, max_bytes=1024 * 1024)
                )
            if raw != actual:
                raise ValueError("Report changed")
            return {
                "version": 1,
                "scope": {
                    "base_url": files.base_url,
                    "app_slug": files.app_slug,
                    "bucket": files.bucket,
                },
                "owner_id": value["owner_id"],
                "key": key,
                "size_bytes": len(raw),
                "sha256": job_hash(raw),
            }

        try:
            await run_job_task(contract, handle, timeout=60, store=store)
        except Exception as error:  # noqa: BLE001 -- expose only a code at the worker boundary
            print(
                json.dumps({"code": getattr(error, "code", "handler_failed")}),
                flush=True,
            )
            raise SystemExit(1) from None


asyncio.run(main())

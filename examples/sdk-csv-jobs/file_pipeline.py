"""Process one native file delivery, sharing the Node app's durable ledger.

Run from a trusted host/adapter with a private delivery JSON file. Native proof
is re-read from the API; supplying a body and invocation ID is not authorization.
"""

import asyncio
import csv
import io
import json
import os
import re
import sys
from pathlib import Path
from urllib.parse import quote

import psycopg
from faas_sdk import JobInput, RequestOptions
from faas_sdk.preview import (
    FaaSClient,
    FileDelivery,
    FileOptions,
    FilePipeline,
    FilePipelineOptions,
    FilePipelineOwner,
    FilePipelineScope,
    NativeFileVerifier,
    PostgresFilePipelineStore,
)
from generated import contract
from psycopg.rows import dict_row


async def main():
    delivery = json.loads(Path(sys.argv[1]).read_text())
    users = json.loads(os.environ["GREGALE_FILE_USERS"])
    async with FaaSClient(
        base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"]
    ) as client:
        files = client.files(
            app_slug=os.environ["GREGALE_FILE_APP"],
            bucket=os.environ["GREGALE_FILE_BUCKET"],
        )
        job_store = await files.job_store(options=FileOptions(timeout=30))
        job = client.define_job(contract, store=job_store)
        scope = FilePipelineScope(
            files.base_url,
            os.environ["GREGALE_FILE_ACCOUNT_ID"],
            os.environ["GREGALE_FILE_APP_ID"],
            files.app_slug,
            files.bucket,
        )

        async def query(sql, params):
            # A fresh autocommit connection makes an acknowledged intent durable
            # before any submission. The SDK owns no driver's ambient transaction.
            async with await psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
            ) as conn:
                await conn.execute("SET statement_timeout='5s'")
                cursor = await conn.execute(sql, params)
                return await cursor.fetchall()

        async def lookup(id, check):
            async def read():
                async with client.async_httpx_client.stream(
                    "GET", "/v1/invocations/" + quote(id, safe="")
                ) as response:
                    response.raise_for_status()
                    raw = bytearray()
                    async for chunk in response.aiter_bytes(chunk_size=8192):
                        check()
                        if len(raw) + len(chunk) > 1024 * 1024:
                            raise ValueError("proof_limit")
                        raw.extend(chunk)
                    return json.loads(raw)

            return await client.arequest(
                read, options=RequestOptions(timeout=0, retry=False)
            )

        async def record_for(event):
            match = re.fullmatch(r"imports/([a-f0-9-]{36})/input.csv", event.key)
            if not match:
                return None
            try:
                record = json.loads(
                    (
                        Path(os.environ["GREGALE_IMPORT_STATE"]) / (match[1] + ".json")
                    ).read_text()
                )
            except FileNotFoundError:
                return None
            if (
                record["key"] != event.key
                or record["session"] != event.receipt_id
                or record["size_bytes"] != event.size_bytes
                or record["owner"] not in users
            ):
                return None
            return record

        async def authorize(event, check):
            record = await record_for(event)
            return (
                FilePipelineOwner(
                    record["owner"], source_digest=record["source_digest"]
                )
                if record
                else None
            )

        async def map_inputs(event, data, owner, check):
            record = await record_for(event)
            if record is None:
                raise ValueError("unowned_file")
            reader = csv.DictReader(
                io.StringIO(data.decode("utf8"), newline=""), strict=True
            )
            if reader.fieldnames != ["row", "email"]:
                raise ValueError("invalid_csv")
            result = []
            for row in reader:
                if (
                    set(row) != {"row", "email"}
                    or re.fullmatch(r"0|[1-9][0-9]*", row["row"]) is None
                ):
                    raise ValueError("invalid_csv")
                result.append(
                    JobInput(
                        record["id"] + ":" + row["row"],
                        {"row": int(row["row"]), "email": row["email"]},
                    )
                )
            return result

        pipeline = FilePipeline(
            name="csv-files-v1",
            revision="1",
            scope=scope,
            prefix="imports/",
            suffix="/input.csv",
            contract=contract,
            job=job,
            files=files,
            job_store=job_store,
            store=PostgresFilePipelineStore(query),
            verify=NativeFileVerifier(scope, lookup),
            authorize=authorize,
            map_inputs=map_inputs,
        )
        if len(sys.argv) > 2 and sys.argv[2] == "reconcile":
            result = await pipeline.reconcile(
                delivery["identity"],
                delivery["run_id"],
                options=FilePipelineOptions(timeout=60, max_bytes=1024 * 1024),
            )
        else:
            result = await pipeline.handle(
                FileDelivery(delivery["body"].encode(), delivery["invocationId"]),
                options=FilePipelineOptions(timeout=60, max_bytes=1024 * 1024),
            )
        print(
            json.dumps(
                {
                    "outcome": result.outcome,
                    "identity": result.receipt.intent.identity
                    if result.receipt
                    else None,
                    "run_id": result.checkpoint.run_id
                    if result.checkpoint
                    else result.run_id,
                }
            ),
            flush=True,
        )


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except Exception as error:  # noqa: BLE001 -- redact adapter errors at the process boundary.
        print(
            json.dumps({"code": getattr(error, "code", "pipeline_failed")}), flush=True
        )
        raise SystemExit(1) from None

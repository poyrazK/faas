"""Fresh backend clients/connections for private local recovery acceptance calls."""

from __future__ import annotations

import json
import os
import re
from contextlib import asynccontextmanager

import psycopg
from compensation import csv_compensation
from faas_sdk.preview import (
    FaaSClient,
    FileOptions,
    PipelineScope,
    PsycopgEventOutboxPool,
)
from psycopg.rows import dict_row


@asynccontextmanager
async def backend(review_base_url="http://127.0.0.1/"):
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))

    async def connect():
        return await psycopg.AsyncConnection.connect(
            os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
        )

    async def query(sql, values):
        parameters = []

        def parameter(match):
            parameters.append(values[int(match[1]) - 1])
            return "%s"

        async with await connect() as conn:
            cursor = await conn.execute(re.sub(r"\$(\d+)", parameter, sql), parameters)
            return list(await cursor.fetchall()) if cursor.description else []

    async def load(id):
        async with await connect() as conn:
            return await (
                await conn.execute(
                    "SELECT * FROM sdk_csv_recovery_owners WHERE source_run_id=%s", [id]
                )
            ).fetchone()

    async with FaaSClient(
        base_url=scope.base_url, token=os.environ["FAAS_TOKEN"]
    ) as client:
        job_store = await client.files(
            app_slug=scope.app_slug, bucket=os.environ["GREGALE_FILE_BUCKET"]
        ).job_store(options=FileOptions(timeout=10))
        yield csv_compensation(
            client=client,
            scope=scope,
            query=query,
            load_owner=load,
            pool=PsycopgEventOutboxPool(connect),
            job_store=job_store,
            review_base_url=review_base_url,
        )

"""Application-owned business labels for the SDK's private authorized inbox."""

import json
import os
import re
from pathlib import Path

import psycopg
from faas_sdk.job_runtime import job_value_hash
from faas_sdk.preview import (
    PipelineScope,
    PostgresWorkInboxStore,
    QueryActor,
    WorkInboxSource,
    create_work_inbox,
)
from psycopg.rows import dict_row


def csv_work_inbox(sources: list[WorkInboxSource[str]], cleanup_name: str):
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))
    sql = json.loads(Path(__file__).with_name("work-inbox-queries.json").read_text())
    cleanup = job_value_hash([scope.to_dict(), "csv_cleanup"])
    refund_workflow = job_value_hash(
        ["gregale.outbound-tool.v1", scope.to_dict(), "csv_refund"]
    )

    async def query(statement, args):
        values = []

        def parameter(match):
            values.append(args[int(match[1]) - 1])
            return "%s"

        async with (
            await psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
            ) as conn,
            conn.transaction(),
        ):
            await conn.execute("SET TRANSACTION READ ONLY")
            await conn.execute("SET LOCAL statement_timeout = '5s'")
            cursor = await conn.execute(
                re.sub(r"\$(\d+)", parameter, statement), values
            )
            return list(await cursor.fetchall())

    async def authorize(_filter, principal, check):
        check()
        return (
            QueryActor(principal, principal) if principal in ("alice", "bob") else None
        )

    async def business_reference(candidate, actor, check):
        check()
        if candidate.kind == "action":
            rows = await query(sql["refund"], [candidate.request_id, actor.owner_id])
        else:
            is_cleanup = candidate.name == cleanup_name
            rows = await query(
                sql["cleanup" if is_cleanup else "refund_workflow"],
                [
                    cleanup if is_cleanup else refund_workflow,
                    candidate.request_id,
                    actor.owner_id,
                ],
            )
        check()
        return rows[0]["business_ref"] if rows else None

    return create_work_inbox(
        scope=scope,
        cursor_key=os.environ["GREGALE_QUERY_CURSOR_KEY"],
        store=PostgresWorkInboxStore(query),
        sources=sources,
        authorize=authorize,
        business_reference=business_reference,
    )

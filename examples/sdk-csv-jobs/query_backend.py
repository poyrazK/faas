"""The same fixed read-only SQL handlers and resource IDs as query.mjs."""

import hashlib
import json
import os
import re
from pathlib import Path

import psycopg
from faas_sdk.job_runtime import JobContract
from faas_sdk.preview import (
    PipelineScope,
    QueryActor,
    QueryResource,
    QueryTool,
    QueryToolError,
    QueryToolMCP,
)
from psycopg.rows import dict_row


def csv_queries():
    specs = json.loads(Path(__file__).with_name("query-contracts.json").read_text())
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))

    async def read_only(sql, args, check):
        check()
        parameters = []

        def parameter(match):
            parameters.append(args[int(match[1]) - 1])
            return "%s"

        async with (
            await psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
            ) as connection,
            connection.transaction(),
        ):
            await connection.execute("SET TRANSACTION READ ONLY")
            await connection.execute("SET LOCAL statement_timeout = '5s'")
            cursor = await connection.execute(
                re.sub(r"\$(\d+)", parameter, sql), parameters
            )
            rows = list(await cursor.fetchall())
            check()
            return rows

    async def owner(id, access, check, payment=False):
        if not isinstance(id, str) or not re.fullmatch(r"[a-f0-9-]{36}", id):
            return None
        rows = await read_only(
            "SELECT i.owner_id FROM sdk_csv_query_imports i JOIN sdk_csv_payments p USING(import_id) WHERE i.import_id=$1::uuid AND i.owner_id=$2 AND i.enabled AND p.owner_id=$2 AND p.enabled"
            if payment
            else "SELECT owner_id FROM sdk_csv_query_imports WHERE import_id=$1::uuid AND owner_id=$2 AND enabled",
            [id, access],
            check,
        )
        return QueryActor(access, rows[0]["owner_id"]) if len(rows) == 1 else None

    async def resource_auth(id, access, check):
        return await owner(id, access, check)

    async def resource_read(id, actor, check):
        rows = await read_only(
            "SELECT failure_report,report_sha256 FROM sdk_csv_query_imports WHERE import_id=$1::uuid AND owner_id=$2 AND enabled",
            [id, actor.owner_id],
            check,
        )
        if (
            not rows
            or hashlib.sha256(rows[0]["failure_report"].encode()).hexdigest()
            != rows[0]["report_sha256"]
        ):
            raise QueryToolError("invalid_output")
        return rows[0]["failure_report"]

    report = QueryResource(
        name="csv_failure_report",
        description="Read the verified import failure summary.",
        revision="v1",
        scope=scope,
        mime_type="text/csv",
        authorize=resource_auth,
        read=resource_read,
    )

    def make(kind, spec):
        async def authorize(input, access, check):
            return await owner(input["import_id"], access, check, kind == "payment")

        async def load(input, context):
            args = [input["import_id"], context.actor.owner_id]
            if kind == "rows":
                rows = await read_only(
                    'SELECT r.input_id,r.row_number,r.email FROM sdk_csv_rows r JOIN sdk_csv_query_imports i ON left(r.input_id,37)=i.import_id::text||\':\' WHERE i.import_id=$1::uuid AND i.owner_id=$2 AND i.enabled AND strpos(r.email,$3)>0 AND ($4::text IS NULL OR r.input_id COLLATE "C">$4::text COLLATE "C") ORDER BY r.input_id COLLATE "C" LIMIT $5',
                    [*args, input["search"], context.after, context.limit + 1],
                    context.check_cancelled,
                )
                return {
                    "items": rows[: context.limit],
                    "after": rows[context.limit - 1]["input_id"]
                    if len(rows) > context.limit
                    else None,
                }
            sql = (
                "SELECT import_id::text,'report'::text AS stage,'failed'::text AS status FROM sdk_csv_query_imports WHERE import_id=$1::uuid AND owner_id=$2 AND enabled"
                if kind == "failure"
                else "SELECT p.payment_id,p.amount_cents AS amount_minor,p.currency,p.state FROM sdk_csv_payments p JOIN sdk_csv_query_imports i USING(import_id) WHERE i.import_id=$1::uuid AND i.owner_id=$2 AND i.enabled AND p.owner_id=$2 AND p.enabled"
            )
            return {
                "items": await read_only(sql, args, context.check_cancelled),
                "after": None,
            }

        return QueryTool(
            JobContract(spec["contract"]),
            name=spec["name"],
            description=spec["description"],
            revision="v1",
            scope=scope,
            cursor_key=os.environ["GREGALE_QUERY_CURSOR_KEY"],
            authorize=authorize,
            load=load,
        )

    tools = {kind: make(kind, spec) for kind, spec in specs.items()}
    return (
        tools,
        report,
        {
            kind: QueryToolMCP(
                tool,
                resources=[report],
                links=lambda item: [{"resource": report.name, "id": item["import_id"]}],
            )
            if kind == "failure"
            else QueryToolMCP(tool)
            for kind, tool in tools.items()
        },
    )

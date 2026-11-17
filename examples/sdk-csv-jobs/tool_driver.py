"""Private local MCP acceptance pipe; the host authenticates the supplied principal."""

from __future__ import annotations

import json
import os
import re
from typing import Any

import psycopg
from faas_sdk.preview import (
    PipelineScope,
    PipelineToolMCP,
    PostgresPipelineGateStore,
    PostgresPipelineToolStore,
)
from pipeline_tool import ToolImport, csv_pipeline_tool
from psycopg.rows import dict_row


async def execute(request):
    if request["action"] == "tool-drive":
        from pipeline_driver import execute as drive

        previous = os.environ.get("GREGALE_PIPELINE_BARRIER")
        if request["arguments"].get("barrier"):
            os.environ["GREGALE_PIPELINE_BARRIER"] = "1"
        else:
            os.environ.pop("GREGALE_PIPELINE_BARRIER", None)
        try:
            return await drive(request["arguments"]["path"])
        finally:
            if previous is None:
                os.environ.pop("GREGALE_PIPELINE_BARRIER", None)
            else:
                os.environ["GREGALE_PIPELINE_BARRIER"] = previous

    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))
    async with await psycopg.AsyncConnection.connect(
        os.environ["DATABASE_URL"],
        autocommit=True,
        row_factory=dict_row,
        options="-c client_encoding=UTF8 -c statement_timeout=5000",
    ) as connection:

        async def query(sql: str, values: list[Any]) -> list[dict[str, Any]]:
            parameters = []

            def parameter(match):
                parameters.append(values[int(match[1]) - 1])
                return "%s"

            cursor = await connection.execute(
                re.sub(r"\$(\d+)", parameter, sql), parameters
            )
            return list(await cursor.fetchall()) if cursor.description else []

        async def load(id):
            row = await (
                await connection.execute(
                    "SELECT owner_id,enabled,input FROM sdk_csv_tool_imports WHERE request_id=%s",
                    [id],
                )
            ).fetchone()
            return ToolImport(**row) if row else None

        tool = csv_pipeline_tool(
            scope=scope,
            store=PostgresPipelineToolStore(query),
            gate_store=PostgresPipelineGateStore(query),
            token=os.environ["FAAS_TOKEN"],
            review_base_url=request["review_base_url"],
            load_import=load,
        )
        mcp = PipelineToolMCP(tool)
        if request["action"] == "tool-list":
            return {"tools": mcp.tools}
        return await mcp.call(
            request["name"], request["arguments"], request["access"], timeout=10
        )

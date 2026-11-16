"""Test-only process adapter for a simultaneous Node/Python decision CAS."""

import asyncio
import json
import os
import re
import sys
from pathlib import Path

import psycopg
from faas_sdk import JobContract
from faas_sdk.preview import (
    NativePipelineGateTransport,
    PipelineGateActor,
    PipelineGateError,
    PipelineGateService,
    PipelineScope,
    PostgresPipelineGateStore,
)
from psycopg.rows import dict_row


async def main():
    fixture = json.loads(Path(sys.argv[1]).read_text())
    scope = PipelineScope.from_dict(fixture["scope"])

    async def query(sql, values):
        ordered = [values[int(m[1]) - 1] for m in re.finditer(r"\$(\d+)", sql)]
        async with await psycopg.AsyncConnection.connect(
            os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
        ) as conn:
            if sql.startswith(
                "UPDATE public.gregale_pipeline_gates SET decision=CASE WHEN expires_at"
            ):
                await conn.execute(
                    "INSERT INTO sdk_pipeline_barrier VALUES ('gate-python') ON CONFLICT DO NOTHING"
                )
                for _ in range(3000):
                    row = await (
                        await conn.execute(
                            "SELECT count(*)::int AS n FROM sdk_pipeline_barrier WHERE driver IN ('gate-node','gate-python')"
                        )
                    ).fetchone()
                    if row["n"] == 2:
                        break
                    await asyncio.sleep(0.01)
                else:
                    raise RuntimeError("decision barrier timeout")
            return await (
                await conn.execute(re.sub(r"\$\d+", "%s", sql), ordered)
            ).fetchall()

    async def authorize(*_):
        return PipelineGateActor(
            actor_id="alice", owner_id="alice", roles=("reviewer",)
        )

    root, review = (
        JobContract(fixture["root_contract"]),
        JobContract(fixture["contract"]),
    )
    service = PipelineGateService(
        scope=scope,
        pipeline_sha256=root.sha256,
        contract=review,
        store=PostgresPipelineGateStore(query),
        transport=NativePipelineGateTransport(
            scope=scope, token=os.environ["FAAS_TOKEN"], contract=root
        ),
        authorize=authorize,
    )
    try:
        view = await service.decide(
            fixture["intent"]["id"],
            binding_sha256=fixture["intent"]["binding_sha256"],
            state="rejected",
            payload={"reason": "simultaneous choice"},
            auth="alice",
            timeout=40,
        )
        print(json.dumps({"ok": True, "state": view.decision.state}))
    except PipelineGateError as error:
        print(json.dumps({"ok": False, "code": error.code}))


asyncio.run(main())

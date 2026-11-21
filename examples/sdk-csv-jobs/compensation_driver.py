"""Private fixture pipe. The caller identity is authenticated by the test host."""

from __future__ import annotations

import os

from compensation_backend import backend
from faas_sdk.preview import CompensationMCP


async def execute(request):
    if request["action"] == "compensation-drive":
        from pipeline_driver import execute as drive

        previous = os.environ.get("GREGALE_PIPELINE_BARRIER")
        if request.get("barrier"):
            os.environ["GREGALE_PIPELINE_BARRIER"] = "1"
        else:
            os.environ.pop("GREGALE_PIPELINE_BARRIER", None)
        try:
            return await drive(request["path"])
        finally:
            if previous is None:
                os.environ.pop("GREGALE_PIPELINE_BARRIER", None)
            else:
                os.environ["GREGALE_PIPELINE_BARRIER"] = previous
    async with backend(request.get("review_base_url", "http://127.0.0.1/")) as (
        recovery,
        _,
    ):
        if request["action"] == "compensation-plan":
            return (
                await recovery.plan(
                    request["source_run_id"], request["access"], timeout=10
                )
            ).to_dict()
        if request["action"] == "compensation-effects":
            return await recovery.effects(
                request["plan_id"], request["access"], timeout=10
            )
        return await CompensationMCP(recovery).call(
            request["name"], request["arguments"], request["access"], timeout=10
        )

"""Private fixture operations; the HTTP/MCP host supplies the authenticated actor."""

import json
from pathlib import Path

from faas_sdk.preview import OutboundActionMCP
from faas_sdk.workflows import run_workflow_invocation
from refund_backend import backend


async def execute(request):
    async with backend(
        request["relay_url"], request.get("review_base_url", "http://127.0.0.1/")
    ) as action:
        if request["action"] == "refund-drive":
            return await run_workflow_invocation(
                action.definition, json.loads(Path(request["path"]).read_text())
            )
        return await OutboundActionMCP(action).call(
            request["name"], request["arguments"], request["access"], timeout=20
        )

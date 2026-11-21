"""Private local acceptance driver, never an HTTP endpoint."""

import asyncio
import json
import os
import sys
from pathlib import Path

from faas_sdk.preview import (
    PipelineScheduleClient,
    PipelineScheduleError,
    PipelineScope,
)
from scheduled_pipeline import csv_pipeline_schedule


async def main():
    request = json.loads(Path(sys.argv[1]).read_text())
    schedule = csv_pipeline_schedule(
        scope=PipelineScope.from_dict(request["scope"]),
        input=request["input"],
        owner_id="alice",
    )

    async def authorize(actor, action):
        return actor == "alice"

    async with PipelineScheduleClient(
        schedule,
        deployment_id=request["deployment_id"],
        token=os.environ["FAAS_TOKEN"],
        authorize=authorize,
    ) as client:
        try:
            await client.history("bob", timeout=10)
        except PipelineScheduleError as error:
            assert error.code == "unauthorized"
        else:
            raise AssertionError("cross-user access")
        view = await client.preview("alice", timeout=10)
        history = await client.history("alice", timeout=10)
        run = await client.attach(request["occurrence_id"], "alice", timeout=10)
        print(
            json.dumps(
                {
                    "identity": schedule.identity,
                    "preview": view.to_dict(),
                    "history": history.to_dict(),
                    "checkpoint": run.checkpoint.to_dict(),
                }
            )
        )


if __name__ == "__main__":
    asyncio.run(main())

"""Verify the saved exact deployments, without the original source or apply call."""

import asyncio
import json
import os
from pathlib import Path

from faas_sdk import FaaSClient, ProjectVerificationOptions, ProjectVerificationTarget
from verification_checks import application_checks


async def main():
    state = Path(
        os.environ.get("GREGALE_PROJECT_STATE", Path(__file__).parent / ".state")
    )
    target = ProjectVerificationTarget.from_dict(
        json.loads((state / "verification-target.json").read_text())
    )
    async with FaaSClient(
        base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"]
    ) as client:
        report = await client.verify_project_application(
            target,
            options=ProjectVerificationOptions(
                timeout=120,
                max_polls=30,
                poll_delay=2,
                checks=application_checks(
                    target, os.environ["GREGALE_APPLICATION_URL"]
                ),
            ),
        )
    print(json.dumps(report.to_dict()))
    if report.outcome != "verified":
        raise SystemExit(1)


if __name__ == "__main__":
    asyncio.run(main())

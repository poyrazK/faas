"""Preview/apply/resume the same project using the installed Python SDK."""

import asyncio
import json
import os
import sys
from pathlib import Path

from faas_sdk import (
    FaaSClient,
    ProjectApplyOptions,
    ProjectPreviewOptions,
    ProjectWorkflowError,
    ReviewedProjectPlan,
)


async def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "preview"
    if mode not in ("preview", "apply", "resume"):
        raise ValueError("Use preview, apply or resume")
    project = os.environ["GREGALE_PROJECT_SLUG"]
    source = Path(
        os.environ.get("GREGALE_PROJECT_SOURCE", Path(__file__).parent / "source")
    )
    state = Path(
        os.environ.get("GREGALE_PROJECT_STATE", Path(__file__).parent / ".state")
    ).resolve()
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    async with FaaSClient(
        base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"]
    ) as client:
        try:
            if mode == "preview":
                if (state / "apply.json").exists():
                    raise SystemExit(
                        "Choose a new GREGALE_PROJECT_STATE for a new review; keep the original plan for resume"
                    )
                plan = await client.preview_project(
                    source,
                    project,
                    app_slugs={"api": project + "-api", "worker": project + "-worker"},
                    options=ProjectPreviewOptions(timeout=120),
                )
                path = state / "plan.json"
                with os.fdopen(
                    os.open(path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600), "w"
                ) as file:
                    json.dump(plan.to_dict(), file)
                preview = plan.to_dict()["preview"]
                print(
                    json.dumps(
                        {
                            "project": project,
                            "canApply": preview["can_apply"],
                            "changes": preview.get("will_deploy"),
                            "managed": preview["managed"],
                            "warnings": preview.get("warnings"),
                        }
                    )
                )
            else:
                plan = ReviewedProjectPlan.from_dict(
                    json.loads((state / "plan.json").read_text())
                )
                options = ProjectApplyOptions(
                    state_path=state / "apply.json", timeout=1200
                )
                result = await (
                    client.apply_project_plan(plan, options=options)
                    if mode == "apply"
                    else client.resume_project_application(plan, options=options)
                )
                print(
                    json.dumps(
                        {
                            "outcome": result.outcome,
                            "attemptId": result.attempt_id,
                            "workloads": [
                                {
                                    "slug": w.slug,
                                    "outcome": w.outcome,
                                    "deploymentId": w.deployment_id,
                                    "code": w.code,
                                }
                                for w in result.workloads
                            ],
                        }
                    )
                )
                if result.outcome != "succeeded":
                    raise SystemExit(1)
        except ProjectWorkflowError as error:
            print(
                json.dumps(
                    {
                        "code": error.code,
                        "phase": error.phase,
                        "attemptId": error.state.attempt_id if error.state else None,
                    }
                ),
                file=sys.stderr,
            )
            raise SystemExit(1) from None


if __name__ == "__main__":
    asyncio.run(main())

"""A fixed, verified import snapshot reviewed on a native nightly schedule."""

from faas_sdk.preview import PipelineSchedule, PipelineScope
from reviewed_pipeline_generated import Input, Output, contract


def csv_pipeline_schedule(
    *, scope: PipelineScope, input: Input, owner_id: str, timezone: str = "UTC"
) -> PipelineSchedule[Input, Output]:
    if (
        input["source"]["owner_id"] != owner_id
        or input["source"]["scope"]["base_url"] != scope.base_url
        or input["source"]["scope"]["app_slug"] != scope.app_slug
    ):
        raise ValueError("invalid_import_owner")
    return PipelineSchedule(
        contract,
        input,
        scope=scope,
        owner_id=owner_id,
        cron="0 2 * * *",
        timezone=timezone,
        overlap="skip",
        catch_up="skip",
    )

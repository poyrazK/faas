"""A typed review of the exact validated import inputs, before any writes."""

from faas_sdk import JobInput
from faas_sdk.preview import (
    JobPipeline,
    JobPipelineContext,
    PipelineError,
    PipelineGateOptions,
    PipelineGateStore,
    PipelineJob,
    PipelineJobStore,
    PipelineScope,
)
from generated import Input as RowInput
from generated import Output as RowOutput
from report_generated import Input as ReportInput
from report_generated import Output as ReportOutput
from review_generated import Input as ReviewInput
from review_generated import contract as review_contract
from reviewed_pipeline_generated import Input, Output, contract
from validate_generated import Input as ValidateInput
from validate_generated import Output as ValidateOutput


def reviewed_csv_pipeline(
    *,
    scope: PipelineScope,
    store: PipelineJobStore,
    gate_store: PipelineGateStore,
    validate: PipelineJob[ValidateInput, ValidateOutput],
    importer: PipelineJob[RowInput, RowOutput],
    report: PipelineJob[ReportInput, ReportOutput],
) -> JobPipeline[Input, Output]:
    async def run(value: Input, wf: JobPipelineContext) -> Output:
        if (
            value["source"]["owner_id"] != wf.owner_id
            or value["source"]["scope"]["base_url"] != scope.base_url
            or value["source"]["scope"]["app_slug"] != scope.app_slug
        ):
            raise PipelineError("scope_mismatch")
        validated = await wf.job(
            "validate", [JobInput(value["import_id"], value)], definition=validate
        )
        rows = validated.results[0].result["rows"]
        review: ReviewInput = {"source": value["source"], "rows": rows}
        outcome = await wf.approval(
            "review",
            review_contract,
            review,
            options=PipelineGateOptions(timeout=60, required_roles=("reviewer",)),
        )
        if outcome.state in ("rejected", "expired", "cancelled"):
            return {"status": outcome.state, "saved": 0, "rejected": 0}
        if outcome.state != "approved":
            raise PipelineError("invalid_result")
        imported = await wf.job(
            "import",
            [JobInput(row["id"], row["value"]) for row in rows],
            definition=importer,
        )
        rejected = [
            r.input_id
            for r in imported.results
            if r.result["rejection"] == "invalid_email"
        ]
        report_input: ReportInput = {
            "import_id": value["import_id"],
            "owner_id": wf.owner_id,
            "rejections": rejected,
        }
        reported = await wf.job(
            "report",
            [JobInput("report:" + value["import_id"], report_input)],
            definition=report,
        )
        return {
            "status": "completed",
            "report": reported.results[0].result,
            "saved": sum(bool(r.result["saved"]) for r in imported.results),
            "rejected": len(rejected),
        }

    return JobPipeline(
        contract,
        scope=scope,
        store=store,
        gate_store=gate_store,
        jobs={"validate": validate, "import": importer, "report": report},
        run=run,
    )

"""Typed parent of reusable reviewed CSV imports. Serve both definitions."""

from batch_generated import Input, Output, Output_children_item, contract
from faas_sdk.preview import (
    JobPipeline,
    JobPipelineContext,
    PipelineChild,
    PipelineChildOptions,
    PipelineChildren,
    PipelineChildrenStore,
    PipelineGateStore,
    PipelineJobStore,
    PipelineScope,
)
from reviewed_pipeline_generated import contract as child_contract


def csv_batch_pipeline(
    *,
    scope: PipelineScope,
    token: str,
    store: PipelineJobStore,
    children_store: PipelineChildrenStore,
    gate_store: PipelineGateStore,
    request_timeout: float = 10,
) -> tuple[JobPipeline[Input, Output], PipelineChildren]:
    child = PipelineChild(contract=child_contract)
    coordinator = PipelineChildren(
        scope=scope,
        parent_contract=contract,
        children=[child],
        store=children_store,
        token=token,
        gate_store=gate_store,
        request_timeout=request_timeout,
    )

    async def run(value: Input, wf: JobPipelineContext) -> Output:
        from faas_sdk import JobInput

        result = await wf.children(
            "imports",
            child,
            [JobInput(i["id"], i["value"]) for i in value["imports"]],
            options=PipelineChildOptions(
                timeout=180,
                concurrency=2,
                failure_policy="collect",
                on_parent_stopped="cancel_children",
                on_deadline="cancel_children",
            ),
        )
        rows: list[Output_children_item] = []
        for c in result.children:
            row: Output_children_item = {"id": c.id, "phase": c.phase}
            if c.run_id:
                row["run_id"] = c.run_id
            if c.output is not None:
                row["status"] = c.output["status"]
                if "report" in c.output:
                    row["report"] = c.output["report"]
            rows.append(row)
        return {
            "saved": sum(
                c.output["saved"] for c in result.children if c.output is not None
            ),
            "rejected": sum(
                c.output["rejected"] for c in result.children if c.output is not None
            ),
            "children": rows,
        }

    return JobPipeline(
        contract, scope=scope, store=store, jobs={}, children=coordinator, run=run
    ), coordinator

"""Typed tool over the same reviewed CSV pipeline and immutable authorized input."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass

from faas_sdk.job_runtime import job_value_hash
from faas_sdk.preview import (
    PipelineGateStore,
    PipelineScope,
    PipelineTool,
    PipelineToolActor,
    PipelineToolRequest,
    PipelineToolStore,
)
from reviewed_pipeline_generated import Input, Output, contract


@dataclass(frozen=True, kw_only=True)
class ToolImport:
    owner_id: str
    enabled: bool
    input: Input


def csv_pipeline_tool(
    *,
    scope: PipelineScope,
    store: PipelineToolStore,
    gate_store: PipelineGateStore,
    token: str,
    review_base_url: str,
    load_import: Callable[[str], Awaitable[ToolImport | None]],
) -> PipelineTool[Input, Output, str]:
    async def authorize(
        request: PipelineToolRequest[Input], actor: str, action: str
    ) -> PipelineToolActor | None:
        record = await load_import(request.request_id)
        if record is None or not record.enabled or record.owner_id != actor:
            return None
        if request.input is not None and job_value_hash(
            contract.input(request.input)
        ) != job_value_hash(contract.input(record.input)):
            return None
        return PipelineToolActor(subject_id=actor, owner_id=record.owner_id)

    return PipelineTool(
        contract,
        name="csv_import",
        description="Validate, review and import an uploaded CSV, then produce a report.",
        scope=scope,
        store=store,
        gate_store=gate_store,
        token=token,
        review_base_url=review_base_url,
        authorize=authorize,
    )

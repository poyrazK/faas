"""Application authority and typed gate shared with the Node CSV pipeline."""

import json
import os
from pathlib import Path

from faas_sdk.preview import (
    FaaSClient,
    FileError,
    FileOptions,
    JobContract,
    NativePipelineGateTransport,
    PipelineGateActor,
    PipelineGateError,
    PipelineGateService,
    PipelineOutputResource,
    PipelineScope,
    PipelineStartPreviewPolicy,
    PipelineStartPreviewSource,
    PipelineTaskInputForm,
    PipelineTool,
    PipelineToolActor,
    PipelineToolError,
    PipelineToolMCP,
    PostgresPipelineGateStore,
    PostgresPipelineToolStore,
    ToolkitMount,
    read_pinned_pipeline_source,
)
from faas_sdk.preview.pipeline_tool_state import identity as tool_identity


def csv_clarification(query, import_id, review_base_url, *, version=1, starts="default"):
    specs = json.loads(Path(__file__).with_name("clarification-contracts.json").read_text())
    revised = specs["revision2"] if version == 2 else None
    name = revised["tool"] if revised else "clarify_csv_columns"
    if revised:
        specs["pipeline"]["name"] = revised["workflow"]
        specs["pipeline"]["input"]["properties"]["source_system"] = revised["sourceSystem"]
        specs["pipeline"]["input"]["required"].append("source_system")
        specs["inputGate"]["output"]["properties"]["email_column"]["enum"] = revised["emailColumns"]
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))
    token = os.environ["FAAS_TOKEN"]
    contract, reply = JobContract(specs["pipeline"]), JobContract(specs["inputGate"])
    gate_store = PostgresPipelineGateStore(query)

    async def owns_import(actor, source=import_id):
        return bool(
            await query(
                "SELECT 1 FROM sdk_csv_query_imports WHERE import_id=$1 AND owner_id=$2 AND enabled",
                [source, actor],
            )
        )

    async def authorize(request, actor, _operation):
        source = request.input["import_id"] if request.input is not None else None
        if not revised and source is None:
            identity = tool_identity(scope, name, actor, request.request_id)
            rows = await query(
                "SELECT record->'input'->'input'->>'import_id' AS import_id FROM sdk_pipeline_runs "
                "WHERE record->'input'->'trigger'->>'identity'=$1 AND record->'input'->>'owner_id'=$2 LIMIT 2",
                [identity, actor],
            )
            if len(rows) > 1:
                return None
            source = rows[0]["import_id"] if rows else None
        source = source or import_id
        if (revised and source != import_id) or not await owns_import(actor, source):
            return None
        return PipelineToolActor(subject_id=actor, owner_id=actor)

    async def authorize_gate(record, _action, actor):
        owner = record.intent["owner_id"]
        source = record.intent["review"]["import_id"]
        if (revised and source != import_id) or not await owns_import(owner, source):
            raise PipelineGateError("forbidden")
        if actor != owner and not await query(
            "SELECT 1 FROM sdk_csv_clarification_reviewers WHERE import_id=$1 AND actor_id=$2 AND enabled",
            [source, actor],
        ):
            raise PipelineGateError("forbidden")
        return PipelineGateActor(actor_id=actor, owner_id=owner, roles=("clarifier",))

    tool = PipelineTool(
        contract,
        name=name,
        description="Select the email column for a CSV import.",
        scope=scope,
        token=token,
        store=PostgresPipelineToolStore(query),
        gate_store=gate_store,
        review_base_url=review_base_url,
        authorize=authorize,
    )
    service = PipelineGateService(
        scope=scope,
        pipeline_sha256=contract.sha256,
        contract=reply,
        store=gate_store,
        transport=NativePipelineGateTransport(scope=scope, token=token, contract=contract),
        authorize=authorize_gate,
    )
    client = FaaSClient(base_url=scope.base_url, token=token, headers={"Connection": "close"})
    files = client.files(app_slug=scope.app_slug, bucket=os.environ["GREGALE_FILE_BUCKET"])

    async def source_for(actor):
        rows = await query(
            "SELECT artifact FROM sdk_csv_mapping_sources WHERE import_id=$1 AND owner_id=$2 AND enabled",
            [import_id, actor],
        )
        return rows[0]["artifact"] if rows else None

    async def read_preview(_input, actor, timeout):
        source = await source_for(actor)
        if source is None:
            raise PipelineToolError("unauthorized")

        async def may_read(pinned):
            return pinned["key"] == f"pipelines/{import_id}/mapping-source.csv" and await source_for(actor) is not None

        data = await read_pinned_pipeline_source(
            files, source, actor, options=FileOptions(timeout=timeout, max_bytes=65536), authorize=may_read,
        )
        source_version = source["sha256"]
        headers = data.decode().split("\n", 1)[0].strip().split(",")
        return PipelineStartPreviewSource(source_version=source_version, pinned_source=source, summary={
            "source_sha256": source_version,
            "email_columns": [column for column in ("contact", "alternate_contact") if column in headers],
            "action": "write_mapping_report",
            "requires_clarification": True,
        })

    async def prepare_replacement(source, actor, timeout):
        if not revised or source["tool"] != name or not await owns_import(actor) or await source_for(actor) is None:
            return None
        rows = await query("SELECT record FROM sdk_pipeline_runs WHERE id=$1", [source["run_id"]])
        if not rows:
            return None
        record = rows[0]["record"]
        envelope = record["input"]
        if (
            record.get("last_error") != "pipeline:source_unavailable" or record["status"] != "failed"
            or record["workflow_name"] != revised["workflow"] or envelope.get("contract_sha256") != contract.sha256
            or envelope.get("owner_id") != actor or envelope.get("trigger", {}).get("identity") != source["receipt_identity"]
            or envelope.get("input", {}).get("import_id") != import_id
        ):
            return None
        try:
            await files.metadata(
                f"pipelines/{import_id}/{source['run_id']}-mapping.csv", options=FileOptions(timeout=timeout),
            )
        except FileError as error:
            if error.status != 404:
                raise
            return {"input": contract.input(envelope["input"]), "previous_source_sha256": envelope.get("pinned_source", {}).get("sha256")}
        return None

    adapter = PipelineToolMCP(
        tool,
        revision={"family": "csv_column_mapping", "starts": starts},
        preview=PipelineStartPreviewPolicy(
            contract=JobContract(specs["startPreview"]),
            signing_key=os.environ["GREGALE_CSV_PREVIEW_KEY"],
            read=read_preview,
        ) if revised else None,
        output_resources=[
            PipelineOutputResource(
                name=revised["artifact"] if revised else "mapping_report",
                description="CSV report identifying the chosen email column.",
                mime_type="text/csv",
                files=files,
                select=lambda output: output.get("report"),
                max_bytes=65536,
            )
        ],
    )
    return name, ToolkitMount(
        adapter,
        resource_scopes={adapter.resource_templates[0]["uriTemplate"]: ["imports:read", "reports:read"]},
        tool_scopes={
            **({name + "_preview": ["imports:preview"]} if revised else {}),
            name + "_start": ["imports:recover"],
            name + "_status": ["imports:read"],
            name + "_result": ["imports:read"],
        },
    ), PipelineTaskInputForm(
        contract=reply,
        service=service,
        scopes=["imports:clarify"],
        message="Which CSV column contains the email address?",
    ), prepare_replacement if revised else None

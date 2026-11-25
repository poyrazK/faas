"""Local HTTP acceptance with a fixture signing key and real starter JWT checks."""

import asyncio
import json
import os
import re
import sys
from pathlib import Path
from types import SimpleNamespace

import psycopg
from faas_sdk.preview import (
    ApplicationToolkit, JobContract, NativePipelineGateTransport, PipelineGateActor, PipelineGateError,
    PipelineGateService, PipelineScope, PipelineTaskApproval, PipelineTaskHandoffSettings,
    PipelineTaskRecoveryPolicy, PipelineTasks, PipelineTool, PipelineToolActor, PipelineToolMCP,
    PostgresPipelineGateStore, PostgresPipelineTaskHandoffStore, PostgresPipelineTaskStore,
    PostgresPipelineToolStore, ToolkitMount,
)
from faas_sdk.job_runtime import job_value_hash
from jwt import PyJWK
from psycopg.rows import dict_row

sys.path.insert(0, sys.argv[1])
from application_toolkit import csv_application_toolkit
from clarification_backend import csv_clarification
from auth import Authenticator
from application_toolkit_host import mount_pipeline_tasks
from server import (
    MCP_CATALOG,
    DrainingHTTPServer,
    MCPHandler,
    install_application_toolkit,
)

config, public_key, recovery_review_url, refund_review_url, relay_url, tasks = json.loads(Path(sys.argv[2]).read_text())


async def query(sql, parameters):
    index = []

    def replace(match):
        index.append(int(match[1]) - 1)
        return "%s"

    statement = re.sub(r"\$(\d+)", replace, sql)
    async with await psycopg.AsyncConnection.connect(os.environ["DATABASE_URL"], row_factory=dict_row) as connection:
        cursor = await connection.execute(statement, [parameters[pos] for pos in index])
        return await cursor.fetchall()


def reviewed_approval(settings):
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))
    root = JobContract(settings["root"])
    review = JobContract(settings["review"])
    token = os.environ["FAAS_TOKEN"]
    gate_store = PostgresPipelineGateStore(query)

    async def authorize_tool(request, actor, _operation):
        rows = await query("SELECT input FROM sdk_csv_tool_imports WHERE request_id=$1 AND owner_id=$2 AND enabled",
                           [request.request_id, actor])
        if not rows or (request.input is not None and job_value_hash(request.input) != job_value_hash(rows[0]["input"])):
            return None
        return PipelineToolActor(subject_id=actor, owner_id=actor)

    async def authorize_gate(record, _action, actor):
        owner = record.intent["owner_id"]
        if actor != owner and not await query(
            "SELECT 1 FROM sdk_csv_approval_members WHERE actor=$1 AND owner=$2 AND enabled", [actor, owner],
        ):
            raise PipelineGateError("forbidden")
        return PipelineGateActor(actor_id=actor, owner_id=owner, roles=("reviewer",))

    tool = PipelineTool(root, name="csv_import", description="Review and import the staged CSV.", scope=scope, token=token,
                        store=PostgresPipelineToolStore(query), gate_store=gate_store,
                        review_base_url="https://app.example.test/review", authorize=authorize_tool)
    toolkit = ApplicationToolkit([ToolkitMount(PipelineToolMCP(tool), tool_scopes={
        "csv_import_start": ["imports:write"], "csv_import_status": ["imports:read"], "csv_import_result": ["imports:read"],
    })])
    service = PipelineGateService(scope=scope, pipeline_sha256=root.sha256, contract=review, store=gate_store,
                                  transport=NativePipelineGateTransport(scope=scope, token=token, contract=root),
                                  authorize=authorize_gate)
    approval = PipelineTaskApproval(contract=review, service=service, title="Import validated rows", scopes=["imports:approve"])
    return toolkit, approval


mounts, input_forms, prepare_replacement = [], {}, None
if tasks and (clarification := tasks.get("clarification")):
    versions = [(1, "retained"), (2, "default")] if clarification.get("publishV2") else [(1, "default")]
    for version, starts in versions:
        name, mount, form, prepare = csv_clarification(
            query, clarification["importId"], clarification["reviewBaseURL"], version=version, starts=starts,
        )
        mounts.append(mount)
        input_forms[name] = form
        if prepare is not None:
            prepare_replacement = prepare
        if name not in tasks["tools"]:
            tasks["tools"].append(name)
approval_handoffs = {}
if tasks and tasks.get("reviewedApproval"):
    toolkit, approval = reviewed_approval(tasks["reviewedApproval"])
    approval_handoffs["csv_import"] = approval
else:
    toolkit = asyncio.run(csv_application_toolkit(recovery_review_url, refund_review_url, relay_url, mounts))
pipeline_tasks = (
    PipelineTasks(
        toolkit,
        namespace=tasks["namespace"],
        owner_key=tasks["ownerKey"],
        tools=tasks["tools"],
        store=PostgresPipelineTaskStore(query),
        input_forms=input_forms,
        handoffs=PipelineTaskHandoffSettings(scopes=["imports:handoff"], store=PostgresPipelineTaskHandoffStore(query))
        if input_forms or approval_handoffs else None,
        approval_handoffs=approval_handoffs,
        publish_manifest=tasks.get("publishManifest", False),
        recovery=PipelineTaskRecoveryPolicy(scopes=["imports:replace"], prepare=prepare_replacement)
        if prepare_replacement else None,
    )
    if tasks
    else None
)

config = install_application_toolkit(config, MCP_CATALOG, toolkit, pipeline_tasks=pipeline_tasks)
auth = Authenticator(config)
jwk = PyJWK(public_key)
auth.jwks_client = SimpleNamespace(get_signing_key_from_jwt=lambda _token: jwk)
task_runtime = mount_pipeline_tasks(config, pipeline_tasks) if pipeline_tasks else None

with DrainingHTTPServer(("127.0.0.1", 0), MCPHandler) as server:
    server.app_config = config
    server.authenticator = auth
    server.task_runtime = task_runtime
    server.subscription_task_runtime = None
    server.expected_catalog_sha256 = ""
    print(json.dumps({"port": server.server_port}), flush=True)
    server.serve_forever()

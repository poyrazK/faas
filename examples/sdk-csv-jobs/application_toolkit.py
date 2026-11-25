"""Register application operations and open fresh backend clients per HTTP call."""

import json
from pathlib import Path

from compensation_backend import backend as recovery_backend
from faas_sdk.preview import (
    ApplicationToolkit,
    CompensationMCP,
    OutboundActionMCP,
    QueryToolMCP,
    ToolkitMount,
    WorkInboxFollowUpOptions,
    WorkInboxSource,
    outbound_work_inbox_source,
    pipeline_work_inbox_source,
)
from query_backend import csv_queries
from refund_backend import backend as refund_backend
from work_inbox_backend import csv_work_inbox


class FreshAdapter:
    def __init__(self, tools, dispatch, pipeline_contract=None):
        self.tools = tools
        self.call = dispatch
        self.pipeline_contract = pipeline_contract


async def csv_application_toolkit(
    recovery_review_url, refund_review_url, relay_url, extra_mounts=()
):
    _, _, adapters = csv_queries()
    rules = json.loads(Path(__file__).with_name("application-toolkit.json").read_text())
    async with recovery_backend(recovery_review_url) as (recovery, _):
        cleanup_adapter = CompensationMCP(recovery)
        cleanup = pipeline_work_inbox_source(
            recovery.tool, receipt_subject_id="csv_cleanup"
        ).selector
    async with refund_backend(relay_url, refund_review_url) as refund:
        refund_tools = OutboundActionMCP(refund).tools
        refund_run = pipeline_work_inbox_source(
            refund.tool, receipt_subject_id="csv_refund"
        ).selector
        refund_intent = outbound_work_inbox_source(refund).selector

    async def cleanup_call(name, args, access, *, timeout):
        async with recovery_backend(recovery_review_url) as (service, _):
            return await CompensationMCP(service).call(
                name, args, access, timeout=timeout
            )

    async def refund_call(name, args, access, *, timeout):
        async with refund_backend(relay_url, refund_review_url) as action:
            return await OutboundActionMCP(action).call(
                name, args, access, timeout=timeout
            )

    async def cleanup_inspect(candidate, actor, scope, access, timeout):
        async with recovery_backend(recovery_review_url) as (service, _):
            return await pipeline_work_inbox_source(
                service.tool, receipt_subject_id="csv_cleanup"
            ).inspect(candidate, actor, scope, access, timeout)

    async def cleanup_lookup(request_id, access, timeout):
        async with recovery_backend(recovery_review_url) as (service, _):
            return await service.tool.find_accepted_run(request_id, access, timeout=timeout)

    async def refund_lookup(request_id, access, timeout):
        async with refund_backend(relay_url, refund_review_url) as action:
            return await action.tool.find_accepted_run(request_id, access, timeout=timeout)

    async def refund_inspect(candidate, actor, scope, access, timeout):
        async with refund_backend(relay_url, refund_review_url) as action:
            source = (
                pipeline_work_inbox_source(action.tool, receipt_subject_id="csv_refund")
                if candidate.kind == "pipeline"
                else outbound_work_inbox_source(action)
            )
            return await source.inspect(candidate, actor, scope, access, timeout)

    inbox = csv_work_inbox(
        [
            WorkInboxSource(cleanup, cleanup_inspect, find_accepted_run=cleanup_lookup),
            WorkInboxSource(refund_run, refund_inspect, find_accepted_run=refund_lookup),
            WorkInboxSource(refund_intent, refund_inspect, "action_csv_refund"),
        ],
        cleanup.name,
    )
    mounts = [
        ToolkitMount(
            adapter,
            tool_scopes=rules[kind],
            resource_scopes={
                adapter.resource_templates[0]["uriTemplate"]: ["reports:read"]
            }
            if kind == "failure"
            else {},
        )
        for kind, adapter in adapters.items()
    ]
    return ApplicationToolkit(
        [
            *mounts,
            *extra_mounts,
            ToolkitMount(QueryToolMCP(inbox), tool_scopes=rules["inbox"]),
            ToolkitMount(
                FreshAdapter(cleanup_adapter.tools, cleanup_call, cleanup_adapter.pipeline_contract), tool_scopes=rules["cleanup"]
            ),
            ToolkitMount(
                FreshAdapter(refund_tools, refund_call), tool_scopes=rules["refund"]
            ),
        ],
        work_inbox_follow_up=WorkInboxFollowUpOptions(inbox, rules["inbox"]["work_inbox"]),
    )

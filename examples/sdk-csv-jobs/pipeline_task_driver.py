"""Resume the saved CSV Task from a separate SDK consumer process."""

import asyncio
import importlib.util
import json
import sys
from contextlib import aclosing

import httpx
from faas_sdk.preview import (
    JobContract,
    PipelineTaskApprovalClient,
    PipelineTaskClarification,
    PipelineTaskClientError,
    PipelineTaskCompletion,
    PipelineTaskHttpTransport,
    PipelineTaskHandoffClient,
    PipelineTaskReviewerInbox,
)


async def main():
    args = json.load(sys.stdin)
    methods = []

    async def record(request):
        methods.append(request.headers["MCP-Method"])

    async with httpx.AsyncClient(
        headers={"Authorization": "Bearer " + args["token"]},
        event_hooks={"request": [record]},
    ) as http:
        transport = PipelineTaskHttpTransport(args["endpoint"], http)
        if args["action"] in ("approval-read", "approval-decide"):
            approval = PipelineTaskApprovalClient(
                args["handoff_id"], contract=JobContract(args["review_contract"]), transport=transport,
            )
            if args["action"] == "approval-read":
                return await approval.get(timeout=10)
            try:
                return await approval.decide(args["view"], args["state"], args["input"], timeout=10)
            except PipelineTaskClientError as error:
                return {"error": error.code}
        spec = importlib.util.spec_from_file_location("csv_task_binding", args["binding"])
        if spec is None or spec.loader is None:
            raise RuntimeError("Generated CSV binding is unavailable")
        binding = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(binding)
        if args["action"] in ("reviewer-inbox-page", "reviewer-inbox-watch"):
            inbox = PipelineTaskReviewerInbox(transport)
            if args["action"] == "reviewer-inbox-page":
                return await inbox.page(page_size=args.get("page_size", 20), cursor=args.get("cursor"), timeout=10)
            async with aclosing(inbox.watch(timeout=15, page_size=args.get("page_size", 20))) as snapshots:
                return await anext(snapshots)
        if args["action"] in ("handoff-read", "handoff-answer"):
            reviewer = PipelineTaskHandoffClient(
                args["handoff_id"], reply_contract=binding.reply_contract, transport=transport,
            )
            if args["action"] == "handoff-read":
                return await reviewer.get(timeout=10)
            await reviewer.answer(args["view"], args["input"], timeout=10)
            return {"submitted": True}
        client = binding.create_client(transport)
        if args["action"] == "preview":
            return await client.preview(args["input"], args["request_id"], binding.preview_contract, timeout=10)
        if args["action"] == "start":
            checkpoint = await client.start(
                args["input"], args["request_id"], preview_token=args.get("preview_token"), timeout=10,
            )
            return {"checkpoint": checkpoint, "methods": methods}
        task = client.resume(args["checkpoint"])
        if args["action"] == "preview-recovery":
            return await task.preview_recovery(args["recovery_request_id"], binding.preview_contract, timeout=10)
        if args["action"] == "confirm-recovery":
            return await task.confirm_recovery(args["recovery_request_id"], args["preview_token"], timeout=10)
        if args["action"] == "observe":
            snapshot = await task.get(timeout=10)
            return {"status": snapshot.status, "progress": snapshot.progress}
        async with aclosing(task.watch(timeout=20)) as snapshots:
            async for snapshot in snapshots:
                if args["action"] == "answer" and isinstance(
                    snapshot, PipelineTaskClarification
                ):
                    key = next(iter(snapshot.forms))
                    await task.answer(
                        snapshot, key, {"email_column": "alternate_contact"}, timeout=10
                    )
                    return {
                        "task_id": task.checkpoint["task_id"],
                        "forms": snapshot.forms,
                        "methods": methods,
                    }
                if args["action"] == "read" and isinstance(
                    snapshot, PipelineTaskCompletion
                ):
                    report = await task.read_artifact(
                        snapshot, "mapping_report", timeout=10
                    )
                    return {
                        "output": snapshot.output,
                        "report": report,
                        "methods": methods,
                    }
    raise RuntimeError("CSV Task did not reach the requested state")


print(json.dumps(asyncio.run(main())))

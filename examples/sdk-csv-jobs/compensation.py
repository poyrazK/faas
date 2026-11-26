"""Typed CSV cleanup: original result evidence plus current owner and row fingerprints."""

from __future__ import annotations

import json
import os
from pathlib import Path

from faas_sdk.job_runtime import JobContract, job_value_hash
from faas_sdk.preview import (
    CompensationActor,
    CompensationCommand,
    CompensationSourceStage,
    PipelineCompensation,
    PipelineJob,
    PostgresCompensationStore,
    PostgresPipelineGateStore,
    PostgresPipelineJobStore,
    PostgresPipelineToolStore,
    compensation_contracts,
)


def csv_compensation(
    *, client, scope, query, load_owner, pool, job_store, review_base_url
):
    business = JobContract(
        json.loads(Path(__file__).with_name("compensation-contract.json").read_text())
    )
    worker = compensation_contracts(business, "csv_cleanup")["worker"]
    original = JobContract(
        json.loads(
            Path(__file__)
            .with_name(
                "reviewed-pipeline-contract.json"
                if os.environ.get("GREGALE_REVIEWED_PIPELINE") == "1"
                else "pipeline-contract.json"
            )
            .read_text()
        )
    )
    stages = {}
    for step, file in (
        ("validate", "validate-contract.json"),
        ("import", "contract.json"),
        ("report", "report-contract.json"),
    ):
        contract = JobContract(json.loads(Path(__file__).with_name(file).read_text()))
        stages[step] = CompensationSourceStage(
            read_only=step != "import",
            definition=PipelineJob(
                contract=contract,
                job=client.define_job(contract, store=job_store),
                job_store=job_store,
            ),
        )

    async def authorize(id, actor, action):
        row = await load_owner(id)
        return (
            CompensationActor(actor_id=actor, owner_id=row["owner_id"])
            if row and row["enabled"] and row["owner_id"] == actor
            else None
        )

    async def execution(plan, run):
        row = await load_owner(plan.source.run_id)
        return bool(row and row["enabled"] and row["owner_id"] == plan.owner_id)

    async def planner(source, actor):
        owner = await load_owner(source.checkpoint.run_id)
        validated = next(s for s in source.stages if s["step"] == "validate")[
            "results"
        ][0]["result"]["rows"]
        inputs = {item["id"]: item["value"] for item in validated}
        imported = next(s for s in source.stages if s["step"] == "import")["results"]
        out = []
        for item in reversed(imported):
            if not item["result"]["saved"]:
                continue
            if not item["input_id"].startswith(str(owner["import_id"]) + ":"):
                raise ValueError("source_import_mismatch")
            row = inputs[item["input_id"]]
            out.append(
                CompensationCommand(
                    id="delete_" + str(item["index"]),
                    effect_id=job_value_hash(item["input_id"]),
                    description="Remove unchanged row " + str(row["row"]),
                    input={
                        "input_id": item["input_id"],
                        "expected_sha256": job_value_hash([row["row"], row["email"]]),
                    },
                )
            )
        return out

    recovery = PipelineCompensation(
        business,
        name="csv_cleanup",
        revision="v1",
        scope=scope,
        source_contract=original,
        source_stages=stages,
        source_store=PostgresPipelineJobStore(query),
        job=PipelineJob(
            contract=worker,
            job=client.define_job(worker, store=job_store),
            job_store=job_store,
            retry_max=2,
        ),
        job_store=PostgresPipelineJobStore(query),
        store=PostgresCompensationStore(query),
        tool_store=PostgresPipelineToolStore(query),
        gate_store=PostgresPipelineGateStore(query),
        token=os.environ["FAAS_TOKEN"],
        review_base_url=review_base_url,
        required_roles=("reviewer",),
        poll_delay=0.05,
        authorize=authorize,
        authorize_execution=execution,
        plan=planner,
    )

    async def authorize_sql(tx, plan, command):
        row = await (
            await tx.execute(
                "SELECT * FROM sdk_csv_recovery_owners WHERE source_run_id=%s FOR SHARE",
                [plan.source.run_id],
            )
        ).fetchone()
        return bool(
            row
            and row["enabled"]
            and row["owner_id"] == plan.owner_id
            and command.input["input_id"].startswith(str(row["import_id"]) + ":")
        )

    async def apply(tx, input, context):
        row = await (
            await tx.execute(
                "SELECT row_number,email FROM sdk_csv_rows WHERE input_id=%s FOR UPDATE",
                [input["input_id"]],
            )
        ).fetchone()
        if row is None:
            return {"state": "already_compensated", "result": {"deleted": False}}
        if (
            job_value_hash([row["row_number"], row["email"]])
            != input["expected_sha256"]
        ):
            return {"state": "needs_review", "result": {"deleted": False}}
        await tx.execute(
            "DELETE FROM sdk_csv_rows WHERE input_id=%s", [input["input_id"]]
        )
        await tx.execute(
            "INSERT INTO sdk_csv_recovery_audit VALUES (%s,1) ON CONFLICT(input_id) DO UPDATE SET actions=sdk_csv_recovery_audit.actions+1",
            [input["input_id"]],
        )
        return {"state": "compensated", "result": {"deleted": True}}

    return recovery, recovery.handler(pool=pool, authorize=authorize_sql, apply=apply)

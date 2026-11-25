"""Local restart adapter; production uses serve_workflows(pipeline.definition)."""

import asyncio
import json
import os
import re
import sys
from pathlib import Path
from typing import Any

import psycopg
from faas_sdk import JobInput
from faas_sdk.preview import (
    FaaSClient,
    FileOptions,
    JobPipeline,
    JobPipelineContext,
    PipelineError,
    PipelineJob,
    PipelineScope,
    PostgresPipelineJobStore,
)
from faas_sdk.workflows import run_workflow_invocation
from generated import contract as import_contract
from pipeline_generated import Input, Output, contract
from psycopg.rows import dict_row
from report_generated import Input as ReportInput
from report_generated import contract as report_contract
from validate_generated import contract as validate_contract


async def execute(request_path: str) -> dict[str, Any]:
    async with FaaSClient(
        base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"]
    ) as client:
        files = client.files(
            app_slug="imports", bucket=os.environ["GREGALE_FILE_BUCKET"]
        )
        job_store = await files.job_store(options=FileOptions(timeout=10))
        scope = PipelineScope.from_dict(
            json.loads(os.environ["GREGALE_PIPELINE_SCOPE"])
        )

        async def query(sql: str, values: list[Any]) -> list[dict[str, Any]]:
            if os.environ.get("GREGALE_CHILDREN_BARRIER") and sql.startswith("UPDATE public.gregale_pipeline_children SET state=") and values[4]:
                async with await psycopg.AsyncConnection.connect(os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row) as barrier:
                    await barrier.execute("INSERT INTO sdk_pipeline_barrier VALUES ('child-python') ON CONFLICT DO NOTHING")
                    for _ in range(3000):
                        row = await (await barrier.execute("SELECT count(*)::int AS n FROM sdk_pipeline_barrier WHERE driver IN ('child-node','child-python')")).fetchone()
                        if row["n"] == 2:
                            break
                        await asyncio.sleep(0.005)
            ordered = [values[int(m[1]) - 1] for m in re.finditer(r"\$(\d+)", sql)]
            statement = re.sub(r"\$\d+", "%s", sql)
            async with await psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
            ) as conn:
                await conn.execute("SET statement_timeout='5s'")
                result = await (await conn.execute(statement, ordered)).fetchall()
                if os.environ.get("GREGALE_PIPELINE_BARRIER") and sql.startswith(
                    "SELECT receipt FROM"
                ):
                    await conn.execute(
                        "INSERT INTO sdk_pipeline_barrier VALUES ('python') ON CONFLICT DO NOTHING"
                    )
                    for _ in range(1000):
                        count = await (
                            await conn.execute(
                                "SELECT count(*)::int AS n FROM sdk_pipeline_barrier WHERE driver IN ('node','python')"
                            )
                        ).fetchone()
                        if count is not None and count["n"] == 2:
                            return result
                        await asyncio.sleep(0.005)
                    raise RuntimeError("Fixture barrier expired")
                return result

        validate = PipelineJob(
            contract=validate_contract,
            job=client.define_job(validate_contract, store=job_store),
            job_store=job_store,
            parallelism=2,
            retry_max=0,
        )
        importer = PipelineJob(
            contract=import_contract,
            job=client.define_job(import_contract, store=job_store),
            job_store=job_store,
            parallelism=2,
            retry_max=0,
        )
        report = PipelineJob(
            contract=report_contract,
            job=client.define_job(report_contract, store=job_store),
            job_store=job_store,
            parallelism=2,
            retry_max=0,
        )

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
            imported = await wf.job(
                "import",
                [
                    JobInput(row["id"], row["value"])
                    for row in validated.results[0].result["rows"]
                ],
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
            report_inputs: list[JobInput[ReportInput]] = [
                JobInput("report:" + value["import_id"], report_input)
            ]
            reported = await wf.job(
                "report",
                report_inputs,
                definition=report,
            )
            return {
                "report": reported.results[0].result,
                "saved": sum(bool(r.result["saved"]) for r in imported.results),
                "rejected": len(rejected),
            }

        pipeline = JobPipeline(
            contract,
            scope=scope,
            store=PostgresPipelineJobStore(query),
            jobs={"validate": validate, "import": importer, "report": report},
            run=run,
        )
        if os.environ.get("GREGALE_REVIEWED_PIPELINE") == "1":
            from faas_sdk.preview import PostgresPipelineGateStore
            from reviewed_pipeline import reviewed_csv_pipeline
            pipeline = reviewed_csv_pipeline(scope=scope, store=PostgresPipelineJobStore(query), gate_store=PostgresPipelineGateStore(query), validate=validate, importer=importer, report=report)
        if os.environ.get("GREGALE_PIPELINE_BARRIER"):
            async with await psycopg.AsyncConnection.connect(
                os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
            ) as conn:
                await conn.execute(
                    "INSERT INTO sdk_pipeline_barrier VALUES ('python-ready') ON CONFLICT DO NOTHING"
                )
                for _ in range(4000):
                    count = await (
                        await conn.execute(
                            "SELECT count(*)::int AS n FROM sdk_pipeline_barrier WHERE driver IN ('node-ready','python-ready')"
                        )
                    ).fetchone()
                    if count is not None and count["n"] == 2:
                        break
                    await asyncio.sleep(0.005)
                else:
                    raise RuntimeError("Fixture driver startup expired")
        request = json.loads(Path(request_path).read_text())
        if request["workflow"] == "csv-batch-pipeline":
            from batch_pipeline import csv_batch_pipeline
            from faas_sdk.preview import PostgresPipelineChildrenStore
            pipeline, _coordinator = csv_batch_pipeline(scope=scope, token=os.environ["FAAS_TOKEN"], store=PostgresPipelineJobStore(query), children_store=PostgresPipelineChildrenStore(query), gate_store=PostgresPipelineGateStore(query), request_timeout=30)
        if request["workflow"].startswith("action-"):
            from refund_backend import backend as refund_backend
            async with refund_backend(os.environ.get("GREGALE_REFUND_RELAY_URL", os.environ.get("GREGALE_OUTBOUND_URL")), os.environ.get("GREGALE_REFUND_REVIEW_URL", "http://127.0.0.1/")) as action:
                return await run_workflow_invocation(action.definition, request)
        if request["workflow"].startswith("recover-"):
            from compensation_backend import backend
            async with backend(os.environ.get("GREGALE_COMP_REVIEW_URL", "http://127.0.0.1/")) as (recovery, _handler):
                return await run_workflow_invocation(recovery.definition, request)
        return await run_workflow_invocation(pipeline.definition, request)


if __name__ == "__main__":
    print(json.dumps(asyncio.run(execute(sys.argv[1]))))

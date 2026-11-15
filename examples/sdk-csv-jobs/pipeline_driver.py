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


async def main() -> None:
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
        print(
            json.dumps(
                await run_workflow_invocation(
                    pipeline.definition, json.loads(Path(sys.argv[1]).read_text())
                )
            )
        )


asyncio.run(main())

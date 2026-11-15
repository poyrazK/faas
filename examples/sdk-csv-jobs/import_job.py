"""Resumes the same checkpoint produced by the Node example."""

import asyncio
import csv
import hashlib
import io
import json
import os
import re
import sys
from dataclasses import asdict
from pathlib import Path

from faas_sdk import (
    FaaSClient,
    JobCheckpoint,
    JobInput,
    JobRunOptions,
    JobSubmitOptions,
    LocalJobStore,
)
from generated import contract


def save(path, checkpoint):
    with os.fdopen(
        os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "w"
    ) as file:
        json.dump(checkpoint.to_dict(), file)


async def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "submit"
    checkpoint = Path(os.environ.get("GREGALE_JOB_CHECKPOINT", "job-run.json"))
    store = (
        LocalJobStore(os.environ["GREGALE_LOCAL_JOB_STORE"])
        if "GREGALE_LOCAL_JOB_STORE" in os.environ
        else None
    )
    async with FaaSClient(
        base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"]
    ) as client:
        job = client.define_job(contract, store=store)
        options = JobRunOptions(timeout=120, max_items=10000)
        if mode == "submit":
            if checkpoint.exists():
                raise ValueError("Choose another checkpoint for a new submission")
            raw = Path(sys.argv[2]).read_bytes()
            if len(raw) > 1024 * 1024:
                raise ValueError("CSV limit")
            if sys.argv[2].endswith(".csv"):
                reader = csv.DictReader(
                    io.StringIO(raw.decode("utf-8"), newline=""), strict=True
                )
                if reader.fieldnames != ["row", "email"]:
                    raise ValueError("Expected row,email header")
                rows = list(reader)
                if not 1 <= len(rows) <= 1000:
                    raise ValueError("CSV limit")
                inputs = []
                for row in rows:
                    if set(row) != {"row", "email"} or not re.fullmatch(
                        r"0|[1-9]\d*", row["row"]
                    ):
                        raise ValueError("Invalid CSV row")
                    number = int(row["row"])
                    value = {"row": number, "email": row["email"]}
                    if str(number) == os.environ.get("GREGALE_CSV_FAIL_ROW"):
                        value["fail_once"] = True
                    inputs.append(
                        JobInput(
                            os.environ.get(
                                "GREGALE_IMPORT_ID",
                                "import-" + hashlib.sha256(raw).hexdigest()[:16],
                            )
                            + ":"
                            + str(number),
                            value,
                        )
                    )
            else:
                inputs = [JobInput(i["id"], i["value"]) for i in json.loads(raw)]
            run = await job.submit(
                inputs,
                options=JobSubmitOptions(
                    timeout=120,
                    max_items=10000,
                    idempotency_key=os.environ["GREGALE_JOB_SUBMISSION_KEY"],
                    parallelism=2,
                    retry_max=0,
                ),
            )
            save(checkpoint, run.checkpoint)
            print(json.dumps({"runId": run.checkpoint.run_id}))
            return
        run = job.resume(JobCheckpoint.from_dict(json.loads(checkpoint.read_text())))
        if mode == "wait":
            print(json.dumps(asdict(await run.wait(options=options))))
        elif mode == "results":
            print(json.dumps([asdict(r) for r in await run.results(options=options)]))
        elif mode == "cancel":
            print(json.dumps(asdict(await run.cancel(options=options))))
        elif mode == "replay":
            replay = await run.retry_failed(
                options=options,
                idempotency_key=os.environ["GREGALE_JOB_SUBMISSION_KEY"],
            )
            destination = Path(
                os.environ.get("GREGALE_JOB_REPLAY_CHECKPOINT", "job-replay.json")
            )
            save(destination, replay.checkpoint)
            print(
                json.dumps(
                    {"runId": replay.checkpoint.run_id, "checkpoint": str(destination)}
                )
            )
        else:
            raise ValueError("Use submit, wait, results, replay or cancel")


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except Exception as error:  # noqa: BLE001 -- omit credentials from process error output.
        print(
            json.dumps({"code": getattr(error, "code", "request_failed")}),
            file=sys.stderr,
        )
        raise SystemExit(1) from None

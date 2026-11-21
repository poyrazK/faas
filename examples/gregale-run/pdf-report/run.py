"""Invoke a published pdf-report@1 task and retain its durable delivery receipt."""
import json
import os
import sys

from faas_sdk.preview import FaaSClient, TaskDestination

from bindings import TaskResult, bind_task


def decode_report(value: object) -> TaskResult:
    if (
        not isinstance(value, dict)
        or type(value.get("words")) is not int
        or type(value.get("pages")) is not int
        or value["words"] < 0
        or value["pages"] < 0
        or type(value.get("preview_truncated")) is not bool
    ):
        raise ValueError("Invalid PDF report result")
    return {"words": value["words"], "pages": value["pages"], "preview_truncated": value["preview_truncated"]}


client = FaaSClient(base_url=os.environ["FAAS_API"], token=os.environ["FAAS_TOKEN"], timeout=120)
try:
    task = bind_task(client, decode_report)
    with open(sys.argv[1], "rb") as source:
        result = task.run(
            {"title": "Customer report"}, files={"input.pdf": source},
            idempotency_key=os.environ["REPORT_REQUEST_ID"],
            deliver=TaskDestination(os.environ["REPORT_APP"], os.environ["REPORT_ROUTE"], {"report.html": "text/html"}),
        )
    print(json.dumps({"run_id": str(result.receipt.id), "result": result.result,
                      "delivery": result.deliveries["report.html"].to_dict()}))
finally:
    client.close()

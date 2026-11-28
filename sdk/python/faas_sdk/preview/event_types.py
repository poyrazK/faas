"""Generate payload types: python -m faas_sdk.preview.event_types CONTRACT LANGUAGE OUTPUT."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any, Literal, cast

from ..job_types import generate_job_types
from .event_contract import EventContract


def generate_event_types(spec: dict[str, Any], language: Literal["typescript", "python"]) -> str:
    spec = EventContract[Any](spec).spec
    generated = generate_job_types(
        {"version": 1, "name": "event-payload", "input": spec["data"], "output": {"type": "null"}}, language
    )
    lines = generated.rstrip().split("\n")[:-1]

    def compact(value: Any) -> str:
        return json.dumps(value, ensure_ascii=False, separators=(",", ":"))

    if language == "typescript":
        replacements = {
            "// Generated from a Gregale job contract.": "// Generated from a Gregale event contract.",
            'import {JobContract} from "@gregale/sdk-node/jobs/runtime";': 'import {EventContract} from "@gregale/sdk-node/preview/events/runtime";',
        }
        return (
            "\n".join(replacements.get(line, line) for line in lines if line != "export type Output = null;")
            + "\nexport const contract = new EventContract<Input>("
            + compact(spec)
            + " as const);\n"
        )
    replacements = {
        "# Generated from a Gregale job contract.": "# Generated from a Gregale event contract.",
        "from faas_sdk.job_runtime import JobContract": "from faas_sdk.preview.event_contract import EventContract",
    }
    return (
        "\n".join(replacements.get(line, line) for line in lines if line != "Output: TypeAlias = None")
        + "\ncontract: EventContract[Input] = EventContract(json.loads("
        + compact(compact(spec))
        + "))\n"
    )


if __name__ == "__main__":
    try:
        source, language, destination = sys.argv[1:]
        if language not in ("typescript", "python"):
            raise ValueError
        Path(destination).write_text(
            generate_event_types(json.loads(Path(source).read_text()), cast(Literal["typescript", "python"], language))
        )
    except Exception:
        raise SystemExit("Use python -m faas_sdk.preview.event_types CONTRACT.json typescript|python OUTPUT") from None

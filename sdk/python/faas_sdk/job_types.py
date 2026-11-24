"""Generate job types: python -m faas_sdk.job_types CONTRACT.json LANGUAGE OUTPUT."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any, Literal, cast

from .job_runtime import JobContract


def generate_job_types(spec: dict[str, Any], language: Literal["typescript", "python"]) -> str:
    if language not in ("typescript", "python"):
        raise ValueError("Unsupported language")
    spec = JobContract[Any, Any](spec).spec

    def compact(value: Any) -> str:
        return json.dumps(value, ensure_ascii=False, separators=(",", ":"))

    if language == "typescript":

        def ts(schema: dict[str, Any]) -> str:
            if "enum" in schema:
                return " | ".join(compact(v) for v in schema["enum"])
            kind = schema["type"]
            if kind == "object":
                return (
                    "{ "
                    + " ".join(
                        "readonly " + compact(k) + ("" if k in schema["required"] else "?") + ": " + ts(v) + ";"
                        for k, v in schema["properties"].items()
                    )
                    + " }"
                )
            if kind == "array":
                return "ReadonlyArray<" + ts(schema["items"]) + ">"
            return "number" if kind == "integer" else str(kind)

        return (
            '// Generated from a Gregale job contract.\nimport {JobContract} from "@gregale/sdk-node/jobs/runtime";\nexport type Input = '
            + ts(spec["input"])
            + ";\nexport type Output = "
            + ts(spec["output"])
            + ";\nexport const contract = new JobContract<Input, Output>("
            + compact(spec)
            + " as const);\n"
        )
    declarations = []

    def py(schema: dict[str, Any], name: str) -> str:
        if "enum" in schema:
            return (
                "Literal["
                + ", ".join(
                    compact(v)
                    if isinstance(v, str)
                    else "None"
                    if v is None
                    else "True"
                    if v is True
                    else "False"
                    if v is False
                    else str(int(v))
                    for v in schema["enum"]
                )
                + "]"
            )
        kind = schema["type"]
        if kind == "object":
            fields = []
            for key, child in schema["properties"].items():
                value = py(child, name + "_" + key)
                fields.append(
                    compact(key) + ": " + (value if key in schema["required"] else "NotRequired[" + value + "]")
                )
            declarations.append(name + " = TypedDict(" + compact(name) + ", {" + ", ".join(fields) + "})")
            return name
        if kind == "array":
            return "list[" + py(schema["items"], name + "_item") + "]"
        return {"integer": "int", "number": "float", "string": "str", "boolean": "bool", "null": "None"}[kind]

    input_type, output_type = py(spec["input"], "Input"), py(spec["output"], "Output")
    if input_type != "Input":
        declarations.append("Input: TypeAlias = " + input_type)
    if output_type != "Output":
        declarations.append("Output: TypeAlias = " + output_type)
    return (
        "# Generated from a Gregale job contract.\n# ruff: noqa: F401\nimport json\nfrom typing import Literal, NotRequired, TypeAlias, TypedDict\nfrom faas_sdk.job_runtime import JobContract\n"
        + "\n".join(declarations)
        + "\ncontract: JobContract[Input, Output] = JobContract(json.loads("
        + compact(compact(spec))
        + "))\n"
    )


if __name__ == "__main__":
    try:
        source, language, destination = sys.argv[1:]
        if language not in ("typescript", "python"):
            raise ValueError
        Path(destination).write_text(
            generate_job_types(json.loads(Path(source).read_text()), cast(Literal["typescript", "python"], language))
        )
    except Exception:
        raise SystemExit(
            "Use python -m faas_sdk.job_types CONTRACT.json typescript|python OUTPUT; unsupported schema keywords are refused."
        ) from None

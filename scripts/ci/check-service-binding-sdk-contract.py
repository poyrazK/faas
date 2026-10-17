#!/usr/bin/env python3
"""Pin the service-binding OpenAPI slice exercised by all three public SDKs."""

import argparse
import difflib
import json
from pathlib import Path

from ruamel.yaml import YAML

ROOT = Path(__file__).resolve().parents[2]
ROOT_MODELS = {"CreateAppRequest", "UpdateAppRequest", "AppResponse", "AppManifest"}
ANNOTATIONS = {"description", "example", "examples", "title", "externalDocs"}


def binding_field(name):
    return (
        name.startswith(
            ("service_binding", "allowed_service_call", "expected_service_binding")
        )
        or name == "service_reliability"
    )


def structural(value):
    if isinstance(value, list):
        return [structural(item) for item in value]
    if not isinstance(value, dict):
        return value
    return {
        key: (
            {name: structural(item) for name, item in child.items()}
            if key == "properties"
            else structural(child)
        )
        for key, child in value.items()
        if key not in ANNOTATIONS
    }


def references(value):
    if isinstance(value, dict):
        if "$ref" in value:
            prefix = "#/components/schemas/"
            if not value["$ref"].startswith(prefix):
                raise ValueError(f"unsupported contract reference: {value['$ref']}")
            yield value["$ref"][len(prefix) :]
        for child in value.values():
            yield from references(child)
    elif isinstance(value, list):
        for child in value:
            yield from references(child)


def contract(document):
    schemas = document["components"]["schemas"]
    selected = {}
    pending = sorted(ROOT_MODELS)
    while pending:
        name = pending.pop()
        if name in selected:
            continue
        schema = structural(schemas[name])
        if name in ROOT_MODELS:
            props = {
                key: value
                for key, value in schema["properties"].items()
                if binding_field(key) or (name == "AppResponse" and key == "manifest")
            }
            schema["properties"] = props
            schema["required"] = sorted(set(schema.get("required", [])) & props.keys())
        selected[name] = schema
        pending.extend(references(schema))
    return {"schemas": selected}


def render(value):
    return json.dumps(value, indent=2, sort_keys=True) + "\n"


def missing_fixture_coverage(snapshot, cases):
    schemas = snapshot["schemas"]
    seen = set()
    enums = {name: set() for name, schema in schemas.items() if "enum" in schema}

    def visit(schema, value, owner=None):
        if "$ref" in schema:
            name = schema["$ref"].rsplit("/", 1)[-1]
            return visit(schemas[name], value, name)
        if value is None:
            return
        if owner in enums:
            enums[owner].add(value)
        if isinstance(value, dict):
            for key, prop in schema.get("properties", {}).items():
                if key in value:
                    seen.add((owner, key))
                    visit(prop, value[key])
            additional = schema.get("additionalProperties")
            if isinstance(additional, dict):
                for child in value.values():
                    visit(additional, child)
        if isinstance(value, list) and "items" in schema:
            for child in value:
                visit(schema["items"], child)

    for fixture in cases["requests"]:
        name = fixture["schema"]
        visit(schemas[name], fixture["body"], name)
    for fixture in cases["responses"]:
        visit(schemas["AppResponse"], fixture["body"], "AppResponse")
    missing = [
        f"{name}.{key}"
        for name, schema in schemas.items()
        for key in schema.get("properties", {})
        if (name, key) not in seen
    ]
    missing.extend(
        f"{name} enum {value!r}"
        for name, values in enums.items()
        for value in schemas[name]["enum"]
        if value not in values
    )
    return sorted(missing)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--openapi", type=Path, default=ROOT / "api/openapi.yaml")
    parser.add_argument(
        "--schema",
        type=Path,
        default=ROOT / "sdk/service-binding-contracts/schema.json",
    )
    parser.add_argument(
        "--cases",
        type=Path,
        default=ROOT / "sdk/service-binding-contracts/cases.json",
    )
    parser.add_argument(
        "--write-schema",
        action="store_true",
        help="explicitly update the reviewed schema snapshot",
    )
    args = parser.parse_args()
    snapshot = contract(YAML(typ="safe").load(args.openapi))
    actual = render(snapshot)
    if args.write_schema:
        args.schema.parent.mkdir(parents=True, exist_ok=True)
        args.schema.write_text(actual)
        return 0
    expected = args.schema.read_text() if args.schema.exists() else ""
    if actual != expected:
        print(
            "".join(
                difflib.unified_diff(
                    expected.splitlines(True),
                    actual.splitlines(True),
                    fromfile=str(args.schema),
                    tofile="OpenAPI service-binding contract",
                )
            )
        )
        print(
            "Service-binding contract drift. Review SDK fields and shared cases, then run:\n"
            "  python scripts/ci/check-service-binding-sdk-contract.py --write-schema\n"
            "Run the Go, Node and Python service-binding contract tests before committing."
        )
        return 1
    missing = missing_fixture_coverage(snapshot, json.loads(args.cases.read_text()))
    if missing:
        print(
            "Shared service-binding fixtures lack coverage for: " + ", ".join(missing)
        )
        return 1
    print("Service-binding SDK contract matches OpenAPI.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

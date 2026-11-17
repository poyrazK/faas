# Generated from a Gregale event contract.
# ruff: noqa: F401
import json
from typing import Literal, NotRequired, TypeAlias, TypedDict
from faas_sdk.preview.event_contract import EventContract
Input = TypedDict("Input", {"import_id": str})
contract: EventContract[Input] = EventContract(json.loads("{\"version\":1,\"type\":\"import.requested\",\"data\":{\"type\":\"object\",\"properties\":{\"import_id\":{\"type\":\"string\",\"minLength\":36,\"maxLength\":36}},\"required\":[\"import_id\"],\"additionalProperties\":false}}"))

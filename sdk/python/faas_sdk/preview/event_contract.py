"""Portable payload contracts for authenticated application CloudEvents."""

from __future__ import annotations

import copy
from typing import Any, Generic, TypeVar

from ..job_runtime import JobContract, JobError, job_value_hash

T = TypeVar("T")


def _event_type(value: Any) -> bool:
    try:
        return (
            isinstance(value, str)
            and 1 <= len(value) <= 256
            and not any(c in value for c in "\r\n\0")
            and value.encode("utf-8").decode("utf-8") == value
        )
    except UnicodeError:
        return False


class EventContract(Generic[T]):
    def __init__(self, spec: dict[str, Any]):
        if (
            not isinstance(spec, dict)
            or set(spec) != {"version", "type", "data"}
            or type(spec["version"]) is not int
            or spec["version"] != 1
            or not _event_type(spec["type"])
        ):
            raise JobError("invalid_contract")
        self._validator = JobContract[T, None](
            {"version": 1, "name": "event-payload", "input": spec["data"], "output": {"type": "null"}}
        )
        self._spec = copy.deepcopy(spec)
        self.sha256 = job_value_hash(self._spec)

    @property
    def spec(self) -> dict[str, Any]:
        return copy.deepcopy(self._spec)

    def input(self, value: Any) -> T:
        return self._validator.input(value)

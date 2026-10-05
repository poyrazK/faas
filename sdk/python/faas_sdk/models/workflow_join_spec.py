from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="WorkflowJoinSpec")


@_attrs_define
class WorkflowJoinSpec:
    """Native branch join. Waits for all dependencies to finish and permits only
    skips caused by false guards, including their descendants. Failed,
    cancelled, unknown and exception-route skips cannot activate a join.
    The first succeeded dependency in output_from order supplies the durable
    output {source: step name, value: original output}. All inactive branches
    skip the join and its continuation. Consumes zero execution attempts.
    Requires 2-128 dependencies and cannot have input, method, when, timeout,
    retry or exception routes, or be/depend on an exception handler.

    """

    output_from: list[str]
    """Every direct dependency exactly once, in explicit selection priority order."""

    def to_dict(self) -> dict[str, Any]:
        output_from = self.output_from

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "output_from": output_from,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        output_from = cast(list[str], d.pop("output_from"))

        workflow_join_spec = cls(
            output_from=output_from,
        )

        return workflow_join_spec

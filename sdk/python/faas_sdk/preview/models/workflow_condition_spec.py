from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="WorkflowConditionSpec")


@_attrs_define
class WorkflowConditionSpec:
    """Bounded scheduled checker. Each 2xx response must be a JSON object with boolean done. A false response becomes the
    next check's input; no compute is held between checks.

    """

    run: str
    """Named app handler that checks the condition."""
    interval: str
    """Delay between checks; at least 1m and no more than the plan wait limit."""
    max_attempts: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run = self.run

        interval = self.interval

        max_attempts = self.max_attempts

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run": run,
                "interval": interval,
                "max_attempts": max_attempts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run = d.pop("run")

        interval = d.pop("interval")

        max_attempts = d.pop("max_attempts")

        workflow_condition_spec = cls(
            run=run,
            interval=interval,
            max_attempts=max_attempts,
        )

        workflow_condition_spec.additional_properties = d
        return workflow_condition_spec

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties

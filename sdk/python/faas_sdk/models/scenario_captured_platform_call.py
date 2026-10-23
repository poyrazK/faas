from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_captured_platform_call_kind import (
    ScenarioCapturedPlatformCallKind,
    check_scenario_captured_platform_call_kind,
)

T = TypeVar("T", bound="ScenarioCapturedPlatformCall")


@_attrs_define
class ScenarioCapturedPlatformCall:
    """A workflow, realtime, Run or cron fire-now call an enrolled app requested through the run-local platform API. It was
    never performed (ADR-20261021150000, ADR-20261021180000, ADR-20261022170348, ADR-20261022194510).

    """

    id: UUID
    workload: str
    kind: ScenarioCapturedPlatformCallKind
    target: str
    """What the call addressed, without platform IDs the run created. A Run start addresses execution:runs, and a
    cron fire-now cron:<id>."""
    name: str
    """The workflow, event or channel name, or a Run's runtime."""
    message: Any
    """The call's compared content, bounded to 64 KiB."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workload = self.workload

        kind: str = self.kind

        target = self.target

        name = self.name

        message = self.message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "workload": workload,
                "kind": kind,
                "target": target,
                "name": name,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workload = d.pop("workload")

        kind = check_scenario_captured_platform_call_kind(d.pop("kind"))

        target = d.pop("target")

        name = d.pop("name")

        message = d.pop("message")

        scenario_captured_platform_call = cls(
            id=id,
            workload=workload,
            kind=kind,
            target=target,
            name=name,
            message=message,
        )

        scenario_captured_platform_call.additional_properties = d
        return scenario_captured_platform_call

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

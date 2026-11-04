from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ScenarioPostgresFaultSpec")


@_attrs_define
class ScenarioPostgresFaultSpec:
    """Separately gated warm rehearsal with one managed worker and one isolated database. Disconnects existing sessions and
    rejects new connections before restoring access. Excludes cold start, suspend/resume and worker termination.

    """

    workload: str
    checkpoint: str
    unavailable_ms: int
    recover_within_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload

        checkpoint = self.checkpoint

        unavailable_ms = self.unavailable_ms

        recover_within_ms = self.recover_within_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload": workload,
                "checkpoint": checkpoint,
                "unavailable_ms": unavailable_ms,
                "recover_within_ms": recover_within_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload = d.pop("workload")

        checkpoint = d.pop("checkpoint")

        unavailable_ms = d.pop("unavailable_ms")

        recover_within_ms = d.pop("recover_within_ms")

        scenario_postgres_fault_spec = cls(
            workload=workload,
            checkpoint=checkpoint,
            unavailable_ms=unavailable_ms,
            recover_within_ms=recover_within_ms,
        )

        scenario_postgres_fault_spec.additional_properties = d
        return scenario_postgres_fault_spec

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

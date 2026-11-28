from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionEnvironmentEvent")


@_attrs_define
class ExecutionEnvironmentEvent:
    """A numbered lifecycle or build diagnostic record for a dependency environment."""

    sequence: int
    stage: str
    message: str
    attempt: int
    created_at: str
    code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        stage = self.stage

        message = self.message

        attempt = self.attempt

        created_at = self.created_at

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "stage": stage,
                "message": message,
                "attempt": attempt,
                "created_at": created_at,
            }
        )
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        stage = d.pop("stage")

        message = d.pop("message")

        attempt = d.pop("attempt")

        created_at = d.pop("created_at")

        code = d.pop("code", UNSET)

        execution_environment_event = cls(
            sequence=sequence,
            stage=stage,
            message=message,
            attempt=attempt,
            created_at=created_at,
            code=code,
        )

        execution_environment_event.additional_properties = d
        return execution_environment_event

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

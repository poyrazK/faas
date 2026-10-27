from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.config_guard_summary_status import ConfigGuardSummaryStatus, check_config_guard_summary_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConfigGuardSummary")


@_attrs_define
class ConfigGuardSummary:
    """The config guard an env or secret write opened or joined."""

    id: UUID
    status: ConfigGuardSummaryStatus
    base_revision: int
    window_minutes: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        base_revision = self.base_revision

        window_minutes = self.window_minutes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "status": status,
                "base_revision": base_revision,
            }
        )
        if window_minutes is not UNSET:
            field_dict["window_minutes"] = window_minutes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_config_guard_summary_status(d.pop("status"))

        base_revision = d.pop("base_revision")

        window_minutes = d.pop("window_minutes", UNSET)

        config_guard_summary = cls(
            id=id,
            status=status,
            base_revision=base_revision,
            window_minutes=window_minutes,
        )

        config_guard_summary.additional_properties = d
        return config_guard_summary

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

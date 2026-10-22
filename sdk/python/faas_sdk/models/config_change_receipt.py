from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.config_guard_summary import ConfigGuardSummary


T = TypeVar("T", bound="ConfigChangeReceipt")


@_attrs_define
class ConfigChangeReceipt:
    """The config revision an env or secret write recorded and the guard watching it (ADR-1451)."""

    revision: int
    guard: ConfigGuardSummary | Unset = UNSET
    """The config guard an env or secret write opened or joined (ADR-1451)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        guard: dict[str, Any] | Unset = UNSET
        if not isinstance(self.guard, Unset):
            guard = self.guard.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revision": revision,
            }
        )
        if guard is not UNSET:
            field_dict["guard"] = guard

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.config_guard_summary import ConfigGuardSummary

        d = dict(src_dict)
        revision = d.pop("revision")

        _guard = d.pop("guard", UNSET)
        guard: ConfigGuardSummary | Unset
        if isinstance(_guard, Unset):
            guard = UNSET
        else:
            guard = ConfigGuardSummary.from_dict(_guard)

        config_change_receipt = cls(
            revision=revision,
            guard=guard,
        )

        config_change_receipt.additional_properties = d
        return config_change_receipt

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

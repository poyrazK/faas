from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="MirrorCleanCondition")


@_attrs_define
class MirrorCleanCondition:
    """Mirror quality gate for a canary stage (issue #1395 B1)."""

    min_invocations: int
    """Minimum completed mirror comparisons required in the window."""
    window_s: int
    """Trailing mirror-summary window in seconds."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        min_invocations = self.min_invocations

        window_s = self.window_s

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "min_invocations": min_invocations,
                "window_s": window_s,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        min_invocations = d.pop("min_invocations")

        window_s = d.pop("window_s")

        mirror_clean_condition = cls(
            min_invocations=min_invocations,
            window_s=window_s,
        )

        mirror_clean_condition.additional_properties = d
        return mirror_clean_condition

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

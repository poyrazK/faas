from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OperatorPlatformFlagConvergence")


@_attrs_define
class OperatorPlatformFlagConvergence:
    """Consumers of a platform flag grouped by whether every instance applied its current version."""

    converged: list[str]
    pending: list[str]
    failed: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        converged = self.converged

        pending = self.pending

        failed = self.failed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "converged": converged,
                "pending": pending,
                "failed": failed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        converged = cast(list[str], d.pop("converged"))

        pending = cast(list[str], d.pop("pending"))

        failed = cast(list[str], d.pop("failed"))

        operator_platform_flag_convergence = cls(
            converged=converged,
            pending=pending,
            failed=failed,
        )

        operator_platform_flag_convergence.additional_properties = d
        return operator_platform_flag_convergence

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

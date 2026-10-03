from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_profile_capability_profile import (
    ExecutionProfileCapabilityProfile,
    check_execution_profile_capability_profile,
)
from ..models.execution_profile_capability_runtimes_item import (
    ExecutionProfileCapabilityRuntimesItem,
    check_execution_profile_capability_runtimes_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_profile_capability_packages import ExecutionProfileCapabilityPackages


T = TypeVar("T", bound="ExecutionProfileCapability")


@_attrs_define
class ExecutionProfileCapability:
    """One dependency profile and its accepted runtime versions."""

    profile: ExecutionProfileCapabilityProfile
    runtimes: list[ExecutionProfileCapabilityRuntimesItem]
    packages: ExecutionProfileCapabilityPackages | Unset = UNSET
    """Immutable preinstalled package versions, when the profile includes packages."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        profile: str = self.profile

        runtimes = []
        for runtimes_item_data in self.runtimes:
            runtimes_item: str = runtimes_item_data
            runtimes.append(runtimes_item)

        packages: dict[str, Any] | Unset = UNSET
        if not isinstance(self.packages, Unset):
            packages = self.packages.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "profile": profile,
                "runtimes": runtimes,
            }
        )
        if packages is not UNSET:
            field_dict["packages"] = packages

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_profile_capability_packages import ExecutionProfileCapabilityPackages

        d = dict(src_dict)
        profile = check_execution_profile_capability_profile(d.pop("profile"))

        runtimes = []
        _runtimes = d.pop("runtimes")
        for runtimes_item_data in _runtimes:
            runtimes_item = check_execution_profile_capability_runtimes_item(runtimes_item_data)

            runtimes.append(runtimes_item)

        _packages = d.pop("packages", UNSET)
        packages: ExecutionProfileCapabilityPackages | Unset
        if isinstance(_packages, Unset):
            packages = UNSET
        else:
            packages = ExecutionProfileCapabilityPackages.from_dict(_packages)

        execution_profile_capability = cls(
            profile=profile,
            runtimes=runtimes,
            packages=packages,
        )

        execution_profile_capability.additional_properties = d
        return execution_profile_capability

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

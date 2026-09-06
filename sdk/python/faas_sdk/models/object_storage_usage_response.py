from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_storage_charge import ObjectStorageCharge
    from ..models.object_storage_policy import ObjectStoragePolicy
    from ..models.object_storage_usage import ObjectStorageUsage


T = TypeVar("T", bound="ObjectStorageUsageResponse")


@_attrs_define
class ObjectStorageUsageResponse:
    """Current UTC-month accounting and operator safety policy."""

    usage: ObjectStorageUsage
    """Account-wide usage and reserved capacity. Costs are EUR millicents, not a customer invoice."""
    policy: ObjectStoragePolicy
    """Operator safety limits; zero values mean unconfigured and block signing."""
    charges: ObjectStorageCharge | Unset = UNSET
    """Current UTC-month customer charge estimate. This is not an invoice until a billing provider posts a line
    item."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        usage = self.usage.to_dict()

        policy = self.policy.to_dict()

        charges: dict[str, Any] | Unset = UNSET
        if not isinstance(self.charges, Unset):
            charges = self.charges.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "usage": usage,
                "policy": policy,
            }
        )
        if charges is not UNSET:
            field_dict["charges"] = charges

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_storage_charge import ObjectStorageCharge
        from ..models.object_storage_policy import ObjectStoragePolicy
        from ..models.object_storage_usage import ObjectStorageUsage

        d = dict(src_dict)
        usage = ObjectStorageUsage.from_dict(d.pop("usage"))

        policy = ObjectStoragePolicy.from_dict(d.pop("policy"))

        _charges = d.pop("charges", UNSET)
        charges: ObjectStorageCharge | Unset
        if isinstance(_charges, Unset):
            charges = UNSET
        else:
            charges = ObjectStorageCharge.from_dict(_charges)

        object_storage_usage_response = cls(
            usage=usage,
            policy=policy,
            charges=charges,
        )

        object_storage_usage_response.additional_properties = d
        return object_storage_usage_response

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

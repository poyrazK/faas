from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_storage_usage_response_billing_mode import (
    ObjectStorageUsageResponseBillingMode,
    check_object_storage_usage_response_billing_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_storage_charge import ObjectStorageCharge
    from ..models.object_storage_policy import ObjectStoragePolicy
    from ..models.object_storage_usage import ObjectStorageUsage


T = TypeVar("T", bound="ObjectStorageUsageResponse")


@_attrs_define
class ObjectStorageUsageResponse:
    """Current UTC-month accounting, customer charge estimate, billing rollout state, and operator safety policy."""

    usage: ObjectStorageUsage
    """Account-wide usage and reserved capacity. Costs are EUR millicents, not a customer invoice."""
    policy: ObjectStoragePolicy
    """Operator safety limits; zero values mean unconfigured and block signing."""
    billing_mode: ObjectStorageUsageResponseBillingMode
    """Whether finalized object-storage charges are disabled, audited locally, or sent to the billing provider."""
    charges: ObjectStorageCharge | Unset = UNSET
    """Current UTC-month customer charge estimate. This is not an invoice until a billing provider posts a line
    item."""
    billing_from: datetime.datetime | Unset = UNSET
    """UTC month boundary at which shadow/live handling starts. Periods before it are never charged."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        usage = self.usage.to_dict()

        policy = self.policy.to_dict()

        billing_mode: str = self.billing_mode

        charges: dict[str, Any] | Unset = UNSET
        if not isinstance(self.charges, Unset):
            charges = self.charges.to_dict()

        billing_from: str | Unset = UNSET
        if not isinstance(self.billing_from, Unset):
            billing_from = self.billing_from.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "usage": usage,
                "policy": policy,
                "billing_mode": billing_mode,
            }
        )
        if charges is not UNSET:
            field_dict["charges"] = charges
        if billing_from is not UNSET:
            field_dict["billing_from"] = billing_from

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_storage_charge import ObjectStorageCharge
        from ..models.object_storage_policy import ObjectStoragePolicy
        from ..models.object_storage_usage import ObjectStorageUsage

        d = dict(src_dict)
        usage = ObjectStorageUsage.from_dict(d.pop("usage"))

        policy = ObjectStoragePolicy.from_dict(d.pop("policy"))

        billing_mode = check_object_storage_usage_response_billing_mode(d.pop("billing_mode"))

        _charges = d.pop("charges", UNSET)
        charges: ObjectStorageCharge | Unset
        if isinstance(_charges, Unset):
            charges = UNSET
        else:
            charges = ObjectStorageCharge.from_dict(_charges)

        _billing_from = d.pop("billing_from", UNSET)
        billing_from: datetime.datetime | Unset
        if isinstance(_billing_from, Unset):
            billing_from = UNSET
        else:
            billing_from = datetime.datetime.fromisoformat(_billing_from)

        object_storage_usage_response = cls(
            usage=usage,
            policy=policy,
            billing_mode=billing_mode,
            charges=charges,
            billing_from=billing_from,
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

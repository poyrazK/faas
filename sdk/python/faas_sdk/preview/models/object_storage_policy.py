from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_storage_policy_accounting_mode import (
    ObjectStoragePolicyAccountingMode,
    check_object_storage_policy_accounting_mode,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectStoragePolicy")


@_attrs_define
class ObjectStoragePolicy:
    """Operator safety limits. Empty accounting_mode requires qualified provider reports including cost. Explicit
    gateway_safety_v1 requires a coverage start, proxied transfers, billing off and no cost ceiling; all other budgets
    remain positive and finite.

    """

    max_account_bytes: int
    max_bucket_bytes: int
    max_account_keys: int
    max_monthly_cost_millicents: int
    max_monthly_requests: int
    max_monthly_egress_bytes: int
    max_monthly_authorizations: int
    max_report_age_seconds: int
    accounting_mode: ObjectStoragePolicyAccountingMode | Unset = UNSET
    gateway_metering_since: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_account_bytes = self.max_account_bytes

        max_bucket_bytes = self.max_bucket_bytes

        max_account_keys = self.max_account_keys

        max_monthly_cost_millicents = self.max_monthly_cost_millicents

        max_monthly_requests = self.max_monthly_requests

        max_monthly_egress_bytes = self.max_monthly_egress_bytes

        max_monthly_authorizations = self.max_monthly_authorizations

        max_report_age_seconds = self.max_report_age_seconds

        accounting_mode: str | Unset = UNSET
        if not isinstance(self.accounting_mode, Unset):
            accounting_mode = self.accounting_mode

        gateway_metering_since: str | Unset = UNSET
        if not isinstance(self.gateway_metering_since, Unset):
            gateway_metering_since = self.gateway_metering_since.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_account_bytes": max_account_bytes,
                "max_bucket_bytes": max_bucket_bytes,
                "max_account_keys": max_account_keys,
                "max_monthly_cost_millicents": max_monthly_cost_millicents,
                "max_monthly_requests": max_monthly_requests,
                "max_monthly_egress_bytes": max_monthly_egress_bytes,
                "max_monthly_authorizations": max_monthly_authorizations,
                "max_report_age_seconds": max_report_age_seconds,
            }
        )
        if accounting_mode is not UNSET:
            field_dict["accounting_mode"] = accounting_mode
        if gateway_metering_since is not UNSET:
            field_dict["gateway_metering_since"] = gateway_metering_since

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_account_bytes = d.pop("max_account_bytes")

        max_bucket_bytes = d.pop("max_bucket_bytes")

        max_account_keys = d.pop("max_account_keys")

        max_monthly_cost_millicents = d.pop("max_monthly_cost_millicents")

        max_monthly_requests = d.pop("max_monthly_requests")

        max_monthly_egress_bytes = d.pop("max_monthly_egress_bytes")

        max_monthly_authorizations = d.pop("max_monthly_authorizations")

        max_report_age_seconds = d.pop("max_report_age_seconds")

        _accounting_mode = d.pop("accounting_mode", UNSET)
        accounting_mode: ObjectStoragePolicyAccountingMode | Unset
        if isinstance(_accounting_mode, Unset):
            accounting_mode = UNSET
        else:
            accounting_mode = check_object_storage_policy_accounting_mode(_accounting_mode)

        _gateway_metering_since = d.pop("gateway_metering_since", UNSET)
        gateway_metering_since: datetime.datetime | Unset
        if isinstance(_gateway_metering_since, Unset):
            gateway_metering_since = UNSET
        else:
            gateway_metering_since = datetime.datetime.fromisoformat(_gateway_metering_since)

        object_storage_policy = cls(
            max_account_bytes=max_account_bytes,
            max_bucket_bytes=max_bucket_bytes,
            max_account_keys=max_account_keys,
            max_monthly_cost_millicents=max_monthly_cost_millicents,
            max_monthly_requests=max_monthly_requests,
            max_monthly_egress_bytes=max_monthly_egress_bytes,
            max_monthly_authorizations=max_monthly_authorizations,
            max_report_age_seconds=max_report_age_seconds,
            accounting_mode=accounting_mode,
            gateway_metering_since=gateway_metering_since,
        )

        object_storage_policy.additional_properties = d
        return object_storage_policy

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

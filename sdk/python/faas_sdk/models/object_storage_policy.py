from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectStoragePolicy")


@_attrs_define
class ObjectStoragePolicy:
    """Operator safety limits; zero values mean unconfigured and block signing."""

    max_account_bytes: int
    max_bucket_bytes: int
    max_account_keys: int
    max_monthly_cost_millicents: int
    max_monthly_requests: int
    max_monthly_egress_bytes: int
    max_monthly_authorizations: int
    max_report_age_seconds: int
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

        object_storage_policy = cls(
            max_account_bytes=max_account_bytes,
            max_bucket_bytes=max_bucket_bytes,
            max_account_keys=max_account_keys,
            max_monthly_cost_millicents=max_monthly_cost_millicents,
            max_monthly_requests=max_monthly_requests,
            max_monthly_egress_bytes=max_monthly_egress_bytes,
            max_monthly_authorizations=max_monthly_authorizations,
            max_report_age_seconds=max_report_age_seconds,
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

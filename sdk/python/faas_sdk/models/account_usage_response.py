from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_usage_response import ManagedPostgresUsageResponse
    from ..models.object_storage_usage_response import ObjectStorageUsageResponse
    from ..models.usage_summary_response import UsageSummaryResponse


T = TypeVar("T", bound="AccountUsageResponse")


@_attrs_define
class AccountUsageResponse:
    """Account-level usage projection across compute and the optional object-storage and managed-PostgreSQL services."""

    month: str
    compute: UsageSummaryResponse
    """Account-level monthly roll-up: included GB-hours, used, overage math, remaining balance, informational usage
    dimensions, and a trailing 30-day daily trend (issue #308). The GB-hours fields drive the overage math; the
    other dimensions are informational."""
    object_storage: ObjectStorageUsageResponse | Unset = UNSET
    """Current UTC-month accounting and operator safety policy."""
    managed_postgres: ManagedPostgresUsageResponse | Unset = UNSET
    """Customer-safe current UTC-month managed PostgreSQL usage and guardrail state."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        month = self.month

        compute = self.compute.to_dict()

        object_storage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.object_storage, Unset):
            object_storage = self.object_storage.to_dict()

        managed_postgres: dict[str, Any] | Unset = UNSET
        if not isinstance(self.managed_postgres, Unset):
            managed_postgres = self.managed_postgres.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "month": month,
                "compute": compute,
            }
        )
        if object_storage is not UNSET:
            field_dict["object_storage"] = object_storage
        if managed_postgres is not UNSET:
            field_dict["managed_postgres"] = managed_postgres

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_usage_response import ManagedPostgresUsageResponse
        from ..models.object_storage_usage_response import ObjectStorageUsageResponse
        from ..models.usage_summary_response import UsageSummaryResponse

        d = dict(src_dict)
        month = d.pop("month")

        compute = UsageSummaryResponse.from_dict(d.pop("compute"))

        _object_storage = d.pop("object_storage", UNSET)
        object_storage: ObjectStorageUsageResponse | Unset
        if isinstance(_object_storage, Unset):
            object_storage = UNSET
        else:
            object_storage = ObjectStorageUsageResponse.from_dict(_object_storage)

        _managed_postgres = d.pop("managed_postgres", UNSET)
        managed_postgres: ManagedPostgresUsageResponse | Unset
        if isinstance(_managed_postgres, Unset):
            managed_postgres = UNSET
        else:
            managed_postgres = ManagedPostgresUsageResponse.from_dict(_managed_postgres)

        account_usage_response = cls(
            month=month,
            compute=compute,
            object_storage=object_storage,
            managed_postgres=managed_postgres,
        )

        account_usage_response.additional_properties = d
        return account_usage_response

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

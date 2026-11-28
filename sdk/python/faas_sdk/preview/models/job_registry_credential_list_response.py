from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.job_registry_credential_response import JobRegistryCredentialResponse


T = TypeVar("T", bound="JobRegistryCredentialListResponse")


@_attrs_define
class JobRegistryCredentialListResponse:
    """Job registry credential metadata plus the per-job quota."""

    credentials: list[JobRegistryCredentialResponse]
    quota_max: int
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        credentials = []
        for credentials_item_data in self.credentials:
            credentials_item = credentials_item_data.to_dict()
            credentials.append(credentials_item)

        quota_max = self.quota_max

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "credentials": credentials,
                "quota_max": quota_max,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_registry_credential_response import JobRegistryCredentialResponse

        d = dict(src_dict)
        credentials = []
        _credentials = d.pop("credentials")
        for credentials_item_data in _credentials:
            credentials_item = JobRegistryCredentialResponse.from_dict(credentials_item_data)

            credentials.append(credentials_item)

        quota_max = d.pop("quota_max")

        count = d.pop("count")

        job_registry_credential_list_response = cls(
            credentials=credentials,
            quota_max=quota_max,
            count=count,
        )

        job_registry_credential_list_response.additional_properties = d
        return job_registry_credential_list_response

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

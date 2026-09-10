from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectStorageComputeBindingSecretKeys")


@_attrs_define
class ObjectStorageComputeBindingSecretKeys:
    """Names of sealed app secrets written for a compute binding; values are never returned."""

    endpoint: str
    region: str
    bucket: str
    access_key_id: str
    secret_access_key: str
    addressing_style: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint = self.endpoint

        region = self.region

        bucket = self.bucket

        access_key_id = self.access_key_id

        secret_access_key = self.secret_access_key

        addressing_style = self.addressing_style

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "endpoint": endpoint,
                "region": region,
                "bucket": bucket,
                "access_key_id": access_key_id,
                "secret_access_key": secret_access_key,
                "addressing_style": addressing_style,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        endpoint = d.pop("endpoint")

        region = d.pop("region")

        bucket = d.pop("bucket")

        access_key_id = d.pop("access_key_id")

        secret_access_key = d.pop("secret_access_key")

        addressing_style = d.pop("addressing_style")

        object_storage_compute_binding_secret_keys = cls(
            endpoint=endpoint,
            region=region,
            bucket=bucket,
            access_key_id=access_key_id,
            secret_access_key=secret_access_key,
            addressing_style=addressing_style,
        )

        object_storage_compute_binding_secret_keys.additional_properties = d
        return object_storage_compute_binding_secret_keys

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

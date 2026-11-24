from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectWorkloadCredentialResponse")


@_attrs_define
class ObjectWorkloadCredentialResponse:
    """A workload S3 credential in the AWS container credential shape, plus where the workload may write."""

    access_key_id: str
    secret_access_key: str
    token: str
    expiration: datetime.datetime
    bucket: str
    key_prefix: str | Unset = UNSET
    endpoint: str | Unset = UNSET
    region: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        access_key_id = self.access_key_id

        secret_access_key = self.secret_access_key

        token = self.token

        expiration = self.expiration.isoformat()

        bucket = self.bucket

        key_prefix = self.key_prefix

        endpoint = self.endpoint

        region = self.region

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "AccessKeyId": access_key_id,
                "SecretAccessKey": secret_access_key,
                "Token": token,
                "Expiration": expiration,
                "Bucket": bucket,
            }
        )
        if key_prefix is not UNSET:
            field_dict["KeyPrefix"] = key_prefix
        if endpoint is not UNSET:
            field_dict["Endpoint"] = endpoint
        if region is not UNSET:
            field_dict["Region"] = region

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        access_key_id = d.pop("AccessKeyId")

        secret_access_key = d.pop("SecretAccessKey")

        token = d.pop("Token")

        expiration = datetime.datetime.fromisoformat(d.pop("Expiration"))

        bucket = d.pop("Bucket")

        key_prefix = d.pop("KeyPrefix", UNSET)

        endpoint = d.pop("Endpoint", UNSET)

        region = d.pop("Region", UNSET)

        object_workload_credential_response = cls(
            access_key_id=access_key_id,
            secret_access_key=secret_access_key,
            token=token,
            expiration=expiration,
            bucket=bucket,
            key_prefix=key_prefix,
            endpoint=endpoint,
            region=region,
        )

        object_workload_credential_response.additional_properties = d
        return object_workload_credential_response

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

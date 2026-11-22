from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_s3_credential_permission import ObjectS3CredentialPermission, check_object_s3_credential_permission
from ..models.object_s3_credential_secret_addressing_style import (
    ObjectS3CredentialSecretAddressingStyle,
    check_object_s3_credential_secret_addressing_style,
)
from ..models.object_s3_credential_status import ObjectS3CredentialStatus, check_object_s3_credential_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectS3CredentialSecret")


@_attrs_define
class ObjectS3CredentialSecret:
    """One-time S3 credential creation response. Configure AWS clients for the returned endpoint, region, and path-style
    addressing.

    """

    id: UUID
    bucket_id: UUID
    access_key_id: str
    label: str
    permission: ObjectS3CredentialPermission
    status: ObjectS3CredentialStatus
    created_at: datetime.datetime
    secret_access_key: str
    endpoint: str
    region: str
    addressing_style: ObjectS3CredentialSecretAddressingStyle
    last_used_at: datetime.datetime | Unset = UNSET
    revoked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        bucket_id = str(self.bucket_id)

        access_key_id = self.access_key_id

        label = self.label

        permission: str = self.permission

        status: str = self.status

        created_at = self.created_at.isoformat()

        secret_access_key = self.secret_access_key

        endpoint = self.endpoint

        region = self.region

        addressing_style: str = self.addressing_style

        last_used_at: str | Unset = UNSET
        if not isinstance(self.last_used_at, Unset):
            last_used_at = self.last_used_at.isoformat()

        revoked_at: str | Unset = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "bucket_id": bucket_id,
                "access_key_id": access_key_id,
                "label": label,
                "permission": permission,
                "status": status,
                "created_at": created_at,
                "secret_access_key": secret_access_key,
                "endpoint": endpoint,
                "region": region,
                "addressing_style": addressing_style,
            }
        )
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        bucket_id = UUID(d.pop("bucket_id"))

        access_key_id = d.pop("access_key_id")

        label = d.pop("label")

        permission = check_object_s3_credential_permission(d.pop("permission"))

        status = check_object_s3_credential_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        secret_access_key = d.pop("secret_access_key")

        endpoint = d.pop("endpoint")

        region = d.pop("region")

        addressing_style = check_object_s3_credential_secret_addressing_style(d.pop("addressing_style"))

        _last_used_at = d.pop("last_used_at", UNSET)
        last_used_at: datetime.datetime | Unset
        if isinstance(_last_used_at, Unset):
            last_used_at = UNSET
        else:
            last_used_at = datetime.datetime.fromisoformat(_last_used_at)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: datetime.datetime | Unset
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = datetime.datetime.fromisoformat(_revoked_at)

        object_s3_credential_secret = cls(
            id=id,
            bucket_id=bucket_id,
            access_key_id=access_key_id,
            label=label,
            permission=permission,
            status=status,
            created_at=created_at,
            secret_access_key=secret_access_key,
            endpoint=endpoint,
            region=region,
            addressing_style=addressing_style,
            last_used_at=last_used_at,
            revoked_at=revoked_at,
        )

        object_s3_credential_secret.additional_properties = d
        return object_s3_credential_secret

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

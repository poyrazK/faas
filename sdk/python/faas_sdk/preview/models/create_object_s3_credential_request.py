from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_object_s3_credential_request_permission import (
    CreateObjectS3CredentialRequestPermission,
    check_create_object_s3_credential_request_permission,
)

T = TypeVar("T", bound="CreateObjectS3CredentialRequest")


@_attrs_define
class CreateObjectS3CredentialRequest:
    """Label and least-privilege access level for a new bucket-scoped S3 credential."""

    label: str
    permission: CreateObjectS3CredentialRequestPermission

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        permission: str = self.permission

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "permission": permission,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        permission = check_create_object_s3_credential_request_permission(d.pop("permission"))

        create_object_s3_credential_request = cls(
            label=label,
            permission=permission,
        )

        return create_object_s3_credential_request

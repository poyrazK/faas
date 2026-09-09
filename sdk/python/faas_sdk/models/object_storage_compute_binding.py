from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.object_s3_credential import ObjectS3Credential
    from ..models.object_storage_compute_binding_secret_keys import ObjectStorageComputeBindingSecretKeys


T = TypeVar("T", bound="ObjectStorageComputeBinding")


@_attrs_define
class ObjectStorageComputeBinding:
    """Provider-neutral app-to-bucket compute binding."""

    id: UUID
    bucket_id: UUID
    scope: str
    prefix: str
    credential: ObjectS3Credential
    """Bucket-scoped Gregale S3 credential metadata. The secret access key is never included in this shape."""
    secret_keys: ObjectStorageComputeBindingSecretKeys
    """Names of sealed app secrets written for a compute binding; values are never returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        bucket_id = str(self.bucket_id)

        scope = self.scope

        prefix = self.prefix

        credential = self.credential.to_dict()

        secret_keys = self.secret_keys.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "bucket_id": bucket_id,
                "scope": scope,
                "prefix": prefix,
                "credential": credential,
                "secret_keys": secret_keys,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_s3_credential import ObjectS3Credential
        from ..models.object_storage_compute_binding_secret_keys import ObjectStorageComputeBindingSecretKeys

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        bucket_id = UUID(d.pop("bucket_id"))

        scope = d.pop("scope")

        prefix = d.pop("prefix")

        credential = ObjectS3Credential.from_dict(d.pop("credential"))

        secret_keys = ObjectStorageComputeBindingSecretKeys.from_dict(d.pop("secret_keys"))

        object_storage_compute_binding = cls(
            id=id,
            bucket_id=bucket_id,
            scope=scope,
            prefix=prefix,
            credential=credential,
            secret_keys=secret_keys,
        )

        object_storage_compute_binding.additional_properties = d
        return object_storage_compute_binding

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

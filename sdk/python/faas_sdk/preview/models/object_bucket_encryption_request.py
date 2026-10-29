from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_encryption import ObjectEncryption


T = TypeVar("T", bound="ObjectBucketEncryptionRequest")


@_attrs_define
class ObjectBucketEncryptionRequest:
    """A non-empty enrolled default policy without per-object context."""

    encryption: ObjectEncryption
    """Owned encryption selection for object writes and upload policies. KMS requires an enrolled Gregale key
    reference; bucket keys apply only to aws:kms. Context is canonical base64 of a bounded JSON object with unique
    string entries."""

    def to_dict(self) -> dict[str, Any]:
        encryption = self.encryption.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "encryption": encryption,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_encryption import ObjectEncryption

        d = dict(src_dict)
        encryption = ObjectEncryption.from_dict(d.pop("encryption"))

        object_bucket_encryption_request = cls(
            encryption=encryption,
        )

        return object_bucket_encryption_request

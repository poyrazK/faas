from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_encryption import ObjectEncryption


T = TypeVar("T", bound="CreateObjectMultipartUploadRequest")


@_attrs_define
class CreateObjectMultipartUploadRequest:
    """Final object identity, total size and optional owned encryption frozen at initiation for a resumable upload."""

    key: str
    size_bytes: int
    content_type: str | Unset = UNSET
    encryption: ObjectEncryption | Unset = UNSET
    """PUT-only owned encryption selection. KMS requires an enrolled Gregale key reference; bucket keys apply only
    to aws:kms. Context is canonical base64 of a bounded JSON object with unique string entries."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        size_bytes = self.size_bytes

        content_type = self.content_type

        encryption: dict[str, Any] | Unset = UNSET
        if not isinstance(self.encryption, Unset):
            encryption = self.encryption.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "size_bytes": size_bytes,
            }
        )
        if content_type is not UNSET:
            field_dict["content_type"] = content_type
        if encryption is not UNSET:
            field_dict["encryption"] = encryption

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_encryption import ObjectEncryption

        d = dict(src_dict)
        key = d.pop("key")

        size_bytes = d.pop("size_bytes")

        content_type = d.pop("content_type", UNSET)

        _encryption = d.pop("encryption", UNSET)
        encryption: ObjectEncryption | Unset
        if isinstance(_encryption, Unset):
            encryption = UNSET
        else:
            encryption = ObjectEncryption.from_dict(_encryption)

        create_object_multipart_upload_request = cls(
            key=key,
            size_bytes=size_bytes,
            content_type=content_type,
            encryption=encryption,
        )

        return create_object_multipart_upload_request

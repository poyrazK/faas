from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_write_receipt_error_code import ObjectWriteReceiptErrorCode, check_object_write_receipt_error_code
from ..models.object_write_receipt_operation import ObjectWriteReceiptOperation, check_object_write_receipt_operation
from ..models.object_write_receipt_status import ObjectWriteReceiptStatus, check_object_write_receipt_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_encryption import ObjectEncryption


T = TypeVar("T", bound="ObjectWriteReceipt")


@_attrs_define
class ObjectWriteReceipt:
    """Public receipt of one tracked PUT, CopyObject or application upload. Contains no credentials, provider placement,
    source identity or recovery lease tokens.

    """

    id: UUID
    bucket_id: UUID
    key: str
    operation: ObjectWriteReceiptOperation
    bytes_: int
    content_type: str
    etag: str
    """Confirmed ETag; empty until completion."""
    status: ObjectWriteReceiptStatus
    created_at: datetime.datetime
    error_code: ObjectWriteReceiptErrorCode | Unset = UNSET
    encryption: ObjectEncryption | Unset = UNSET
    """Owned encryption selection for object writes and upload policies. KMS requires an enrolled Gregale key
    reference; bucket keys apply only to aws:kms. Context is canonical base64 of a bounded JSON object with unique
    string entries."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        bucket_id = str(self.bucket_id)

        key = self.key

        operation: str = self.operation

        bytes_ = self.bytes_

        content_type = self.content_type

        etag = self.etag

        status: str = self.status

        created_at = self.created_at.isoformat()

        error_code: str | Unset = UNSET
        if not isinstance(self.error_code, Unset):
            error_code = self.error_code

        encryption: dict[str, Any] | Unset = UNSET
        if not isinstance(self.encryption, Unset):
            encryption = self.encryption.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "bucket_id": bucket_id,
                "key": key,
                "operation": operation,
                "bytes": bytes_,
                "content_type": content_type,
                "etag": etag,
                "status": status,
                "created_at": created_at,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if encryption is not UNSET:
            field_dict["encryption"] = encryption

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_encryption import ObjectEncryption

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        bucket_id = UUID(d.pop("bucket_id"))

        key = d.pop("key")

        operation = check_object_write_receipt_operation(d.pop("operation"))

        bytes_ = d.pop("bytes")

        content_type = d.pop("content_type")

        etag = d.pop("etag")

        status = check_object_write_receipt_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _error_code = d.pop("error_code", UNSET)
        error_code: ObjectWriteReceiptErrorCode | Unset
        if isinstance(_error_code, Unset):
            error_code = UNSET
        else:
            error_code = check_object_write_receipt_error_code(_error_code)

        _encryption = d.pop("encryption", UNSET)
        encryption: ObjectEncryption | Unset
        if isinstance(_encryption, Unset):
            encryption = UNSET
        else:
            encryption = ObjectEncryption.from_dict(_encryption)

        object_write_receipt = cls(
            id=id,
            bucket_id=bucket_id,
            key=key,
            operation=operation,
            bytes_=bytes_,
            content_type=content_type,
            etag=etag,
            status=status,
            created_at=created_at,
            error_code=error_code,
            encryption=encryption,
        )

        object_write_receipt.additional_properties = d
        return object_write_receipt

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

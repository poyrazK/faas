from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.object_bucket import ObjectBucket
    from ..models.object_storage_compute_binding import ObjectStorageComputeBinding


T = TypeVar("T", bound="DevBucketResponse")


@_attrs_define
class DevBucketResponse:
    """Safe metadata for an active automatic developer bucket binding. No credential secret or provider placement is
    returned.

    """

    bucket: ObjectBucket
    """Logical bucket metadata without upstream credentials or placement details."""
    binding: ObjectStorageComputeBinding
    """Provider-neutral app-to-bucket compute binding. Rotation responses and binding lists set rotation_pending
    while the previous key remains valid during a rolling runtime refresh."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bucket = self.bucket.to_dict()

        binding = self.binding.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bucket": bucket,
                "binding": binding,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_bucket import ObjectBucket
        from ..models.object_storage_compute_binding import ObjectStorageComputeBinding

        d = dict(src_dict)
        bucket = ObjectBucket.from_dict(d.pop("bucket"))

        binding = ObjectStorageComputeBinding.from_dict(d.pop("binding"))

        dev_bucket_response = cls(
            bucket=bucket,
            binding=binding,
        )

        dev_bucket_response.additional_properties = d
        return dev_bucket_response

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

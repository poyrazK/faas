from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="PutCellImageReleaseRequest")


@_attrs_define
class PutCellImageReleaseRequest:
    """Digest-pinned OCI image release using the existing regional app configuration."""

    image: str

    def to_dict(self) -> dict[str, Any]:
        image = self.image

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "image": image,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        image = d.pop("image")

        put_cell_image_release_request = cls(
            image=image,
        )

        return put_cell_image_release_request

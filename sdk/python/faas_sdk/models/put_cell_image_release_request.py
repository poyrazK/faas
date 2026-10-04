from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.cell_image_release_ordering import CellImageReleaseOrdering


T = TypeVar("T", bound="PutCellImageReleaseRequest")


@_attrs_define
class PutCellImageReleaseRequest:
    """Digest-pinned OCI image release using the existing regional app configuration."""

    image: str
    ordering: CellImageReleaseOrdering | Unset = UNSET
    """Immutable global app stream and increasing generation. Older first submissions cannot supersede an accepted
    newer generation."""

    def to_dict(self) -> dict[str, Any]:
        image = self.image

        ordering: dict[str, Any] | Unset = UNSET
        if not isinstance(self.ordering, Unset):
            ordering = self.ordering.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "image": image,
            }
        )
        if ordering is not UNSET:
            field_dict["ordering"] = ordering

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.cell_image_release_ordering import CellImageReleaseOrdering

        d = dict(src_dict)
        image = d.pop("image")

        _ordering = d.pop("ordering", UNSET)
        ordering: CellImageReleaseOrdering | Unset
        if isinstance(_ordering, Unset):
            ordering = UNSET
        else:
            ordering = CellImageReleaseOrdering.from_dict(_ordering)

        put_cell_image_release_request = cls(
            image=image,
            ordering=ordering,
        )

        return put_cell_image_release_request

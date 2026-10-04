from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="CellImageReleaseOrdering")


@_attrs_define
class CellImageReleaseOrdering:
    """Immutable global app stream and increasing generation. Older first submissions cannot supersede an accepted newer
    generation.

    """

    global_app_id: UUID
    generation: int

    def to_dict(self) -> dict[str, Any]:
        global_app_id = str(self.global_app_id)

        generation = self.generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "global_app_id": global_app_id,
                "generation": generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        global_app_id = UUID(d.pop("global_app_id"))

        generation = d.pop("generation")

        cell_image_release_ordering = cls(
            global_app_id=global_app_id,
            generation=generation,
        )

        return cell_image_release_ordering

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_tagging_result_tags import ObjectTaggingResultTags


T = TypeVar("T", bound="ObjectTaggingResult")


@_attrs_define
class ObjectTaggingResult:
    """Acknowledged object tags and optional owned public version identity."""

    tags: ObjectTaggingResultTags
    """Acknowledged complete tag set; native provider IDs and private completion metadata are never exposed."""
    version_id: str | Unset = UNSET
    """Public version UUID or null. Omitted for unversioned or legacy current-object providers."""

    def to_dict(self) -> dict[str, Any]:
        tags = self.tags.to_dict()

        version_id = self.version_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "tags": tags,
            }
        )
        if version_id is not UNSET:
            field_dict["version_id"] = version_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_tagging_result_tags import ObjectTaggingResultTags

        d = dict(src_dict)
        tags = ObjectTaggingResultTags.from_dict(d.pop("tags"))

        version_id = d.pop("version_id", UNSET)

        object_tagging_result = cls(
            tags=tags,
            version_id=version_id,
        )

        return object_tagging_result

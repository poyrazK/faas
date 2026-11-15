from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="TaskToolReleaseIntentCheck")


@_attrs_define
class TaskToolReleaseIntentCheck:
    """Freshly computed local Task fingerprint supplied for permanent native drift detection."""

    release_fingerprint: str

    def to_dict(self) -> dict[str, Any]:
        release_fingerprint = self.release_fingerprint

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "release_fingerprint": release_fingerprint,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        release_fingerprint = d.pop("release_fingerprint")

        task_tool_release_intent_check = cls(
            release_fingerprint=release_fingerprint,
        )

        return task_tool_release_intent_check

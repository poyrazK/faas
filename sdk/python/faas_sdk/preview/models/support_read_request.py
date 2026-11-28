from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="SupportReadRequest")


@_attrs_define
class SupportReadRequest:
    """Observed notification boundary; future rows are never acknowledged."""

    through_sequence: int

    def to_dict(self) -> dict[str, Any]:
        through_sequence = self.through_sequence

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "through_sequence": through_sequence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        through_sequence = d.pop("through_sequence")

        support_read_request = cls(
            through_sequence=through_sequence,
        )

        return support_read_request

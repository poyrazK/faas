from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeDeadLetterPreviewRequest")


@_attrs_define
class RealtimeDeadLetterPreviewRequest:
    """Realtime Dead Letter Preview Request."""

    failure_codes: list[str] | Unset = UNSET
    """Empty selects all codes; otherwise exact case-sensitive matches."""
    after_sequence: str | Unset = "0"
    """Exclusive lower bound within signed 64-bit sequence range."""
    through_sequence: str | Unset = UNSET
    """Optional inclusive upper bound greater than after_sequence."""
    limit: int | Unset = 32
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        failure_codes: list[str] | Unset = UNSET
        if not isinstance(self.failure_codes, Unset):
            failure_codes = self.failure_codes

        after_sequence = self.after_sequence

        through_sequence = self.through_sequence

        limit = self.limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if failure_codes is not UNSET:
            field_dict["failure_codes"] = failure_codes
        if after_sequence is not UNSET:
            field_dict["after_sequence"] = after_sequence
        if through_sequence is not UNSET:
            field_dict["through_sequence"] = through_sequence
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        failure_codes = cast(list[str], d.pop("failure_codes", UNSET))

        after_sequence = d.pop("after_sequence", UNSET)

        through_sequence = d.pop("through_sequence", UNSET)

        limit = d.pop("limit", UNSET)

        realtime_dead_letter_preview_request = cls(
            failure_codes=failure_codes,
            after_sequence=after_sequence,
            through_sequence=through_sequence,
            limit=limit,
        )

        realtime_dead_letter_preview_request.additional_properties = d
        return realtime_dead_letter_preview_request

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

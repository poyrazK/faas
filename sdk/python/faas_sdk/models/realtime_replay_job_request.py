from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeReplayJobRequest")


@_attrs_define
class RealtimeReplayJobRequest:
    """Realtime Replay Job Request."""

    rate: int
    failure_codes: list[str] | Unset = UNSET
    after_sequence: str | Unset = "0"
    through_sequence: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rate = self.rate

        failure_codes: list[str] | Unset = UNSET
        if not isinstance(self.failure_codes, Unset):
            failure_codes = self.failure_codes

        after_sequence = self.after_sequence

        through_sequence = self.through_sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rate": rate,
            }
        )
        if failure_codes is not UNSET:
            field_dict["failure_codes"] = failure_codes
        if after_sequence is not UNSET:
            field_dict["after_sequence"] = after_sequence
        if through_sequence is not UNSET:
            field_dict["through_sequence"] = through_sequence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rate = d.pop("rate")

        failure_codes = cast(list[str], d.pop("failure_codes", UNSET))

        after_sequence = d.pop("after_sequence", UNSET)

        through_sequence = d.pop("through_sequence", UNSET)

        realtime_replay_job_request = cls(
            rate=rate,
            failure_codes=failure_codes,
            after_sequence=after_sequence,
            through_sequence=through_sequence,
        )

        realtime_replay_job_request.additional_properties = d
        return realtime_replay_job_request

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

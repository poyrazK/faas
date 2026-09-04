from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AdvanceCanaryRequest")


@_attrs_define
class AdvanceCanaryRequest:
    """Body for POST /v1/deployments/{id}/canary/advance (issue #976 / ADR-122). expected_step is the persisted canary step
    observed by the progression worker; APID derives the next traffic percentage and rejects stale observations.

    """

    expected_step: int
    """The canary_step the caller read before requesting the next stage."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_step = self.expected_step

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_step": expected_step,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_step = d.pop("expected_step")

        advance_canary_request = cls(
            expected_step=expected_step,
        )

        advance_canary_request.additional_properties = d
        return advance_canary_request

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

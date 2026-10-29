from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_query import ProfileQuery


T = TypeVar("T", bound="ProfileCompareRequest")


@_attrs_define
class ProfileCompareRequest:
    """Baseline and candidate deployment CPU selections."""

    baseline: ProfileQuery
    """Authorized deployment CPU capture window."""
    candidate: ProfileQuery
    """Authorized deployment CPU capture window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "baseline": baseline,
                "candidate": candidate,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_query import ProfileQuery

        d = dict(src_dict)
        baseline = ProfileQuery.from_dict(d.pop("baseline"))

        candidate = ProfileQuery.from_dict(d.pop("candidate"))

        profile_compare_request = cls(
            baseline=baseline,
            candidate=candidate,
        )

        profile_compare_request.additional_properties = d
        return profile_compare_request

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

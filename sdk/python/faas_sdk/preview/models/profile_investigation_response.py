from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_investigation import ProfileInvestigation
    from ..models.profile_investigation_window_status import ProfileInvestigationWindowStatus


T = TypeVar("T", bound="ProfileInvestigationResponse")


@_attrs_define
class ProfileInvestigationResponse:
    """Saved metadata and authenticated dashboard link, with explicit per-window eligibility."""

    saved: ProfileInvestigation
    """Persistent app-owned selections and notes; no profile samples are stored. Up to 50 investigations per app."""
    url: str
    """Relative dashboard link that requires an authenticated account with access to this app."""
    baseline_status: ProfileInvestigationWindowStatus
    """Query eligibility under the current plan and installation. Retained does not guarantee that samples exist;
    expiry never implies zero CPU usage."""
    candidate_status: ProfileInvestigationWindowStatus
    """Query eligibility under the current plan and installation. Retained does not guarantee that samples exist;
    expiry never implies zero CPU usage."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        saved = self.saved.to_dict()

        url = self.url

        baseline_status = self.baseline_status.to_dict()

        candidate_status = self.candidate_status.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "saved": saved,
                "url": url,
                "baseline_status": baseline_status,
                "candidate_status": candidate_status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_investigation import ProfileInvestigation
        from ..models.profile_investigation_window_status import ProfileInvestigationWindowStatus

        d = dict(src_dict)
        saved = ProfileInvestigation.from_dict(d.pop("saved"))

        url = d.pop("url")

        baseline_status = ProfileInvestigationWindowStatus.from_dict(d.pop("baseline_status"))

        candidate_status = ProfileInvestigationWindowStatus.from_dict(d.pop("candidate_status"))

        profile_investigation_response = cls(
            saved=saved,
            url=url,
            baseline_status=baseline_status,
            candidate_status=candidate_status,
        )

        profile_investigation_response.additional_properties = d
        return profile_investigation_response

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

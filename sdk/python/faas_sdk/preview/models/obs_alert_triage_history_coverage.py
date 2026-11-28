from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_triage_history_coverage_reason import (
    ObsAlertTriageHistoryCoverageReason,
    check_obs_alert_triage_history_coverage_reason,
)
from ..models.obs_alert_triage_history_coverage_state import (
    ObsAlertTriageHistoryCoverageState,
    check_obs_alert_triage_history_coverage_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertTriageHistoryCoverage")


@_attrs_define
class ObsAlertTriageHistoryCoverage:
    """Retained receipt counts across revisions one through snapshot_revision independent of page truncation."""

    state: ObsAlertTriageHistoryCoverageState
    available_entries: int
    missing_entries: int
    reason: ObsAlertTriageHistoryCoverageReason | Unset = UNSET
    oldest_available_revision: int | Unset = UNSET
    newest_available_revision: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        available_entries = self.available_entries

        missing_entries = self.missing_entries

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        oldest_available_revision = self.oldest_available_revision

        newest_available_revision = self.newest_available_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "available_entries": available_entries,
                "missing_entries": missing_entries,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if oldest_available_revision is not UNSET:
            field_dict["oldest_available_revision"] = oldest_available_revision
        if newest_available_revision is not UNSET:
            field_dict["newest_available_revision"] = newest_available_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_alert_triage_history_coverage_state(d.pop("state"))

        available_entries = d.pop("available_entries")

        missing_entries = d.pop("missing_entries")

        _reason = d.pop("reason", UNSET)
        reason: ObsAlertTriageHistoryCoverageReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_alert_triage_history_coverage_reason(_reason)

        oldest_available_revision = d.pop("oldest_available_revision", UNSET)

        newest_available_revision = d.pop("newest_available_revision", UNSET)

        obs_alert_triage_history_coverage = cls(
            state=state,
            available_entries=available_entries,
            missing_entries=missing_entries,
            reason=reason,
            oldest_available_revision=oldest_available_revision,
            newest_available_revision=newest_available_revision,
        )

        obs_alert_triage_history_coverage.additional_properties = d
        return obs_alert_triage_history_coverage

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

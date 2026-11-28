from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_collection_failure_diagnostics_state import (
    ObsAlertCollectionFailureDiagnosticsState,
    check_obs_alert_collection_failure_diagnostics_state,
)
from ..models.obs_alert_collection_failure_reason import (
    ObsAlertCollectionFailureReason,
    check_obs_alert_collection_failure_reason,
)

if TYPE_CHECKING:
    from ..models.obs_alert_collection_rejection import ObsAlertCollectionRejection


T = TypeVar("T", bound="ObsAlertCollectionFailure")


@_attrs_define
class ObsAlertCollectionFailure:
    """One immutable failed-attempt receipt with optional sanitized evidence captured from its rejected source snapshot."""

    attempted_at: datetime.datetime
    reason: ObsAlertCollectionFailureReason
    diagnostics_state: ObsAlertCollectionFailureDiagnosticsState
    """Recorded only when typed historical rejection evidence is present; unknown never implies healthy rules."""
    diagnostics: None | ObsAlertCollectionRejection
    """Null for old receipts or failures without a rejected readable source; present only for snapshot_rejected."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_alert_collection_rejection import ObsAlertCollectionRejection

        attempted_at = self.attempted_at.isoformat()

        reason: str = self.reason

        diagnostics_state: str = self.diagnostics_state

        diagnostics: dict[str, Any] | None
        if isinstance(self.diagnostics, ObsAlertCollectionRejection):
            diagnostics = self.diagnostics.to_dict()
        else:
            diagnostics = self.diagnostics

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempted_at": attempted_at,
                "reason": reason,
                "diagnostics_state": diagnostics_state,
                "diagnostics": diagnostics,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_collection_rejection import ObsAlertCollectionRejection

        d = dict(src_dict)
        attempted_at = datetime.datetime.fromisoformat(d.pop("attempted_at"))

        reason = check_obs_alert_collection_failure_reason(d.pop("reason"))

        diagnostics_state = check_obs_alert_collection_failure_diagnostics_state(d.pop("diagnostics_state"))

        def _parse_diagnostics(data: object) -> None | ObsAlertCollectionRejection:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                diagnostics_type_0 = ObsAlertCollectionRejection.from_dict(data)

                return diagnostics_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsAlertCollectionRejection, data)

        diagnostics = _parse_diagnostics(d.pop("diagnostics"))

        obs_alert_collection_failure = cls(
            attempted_at=attempted_at,
            reason=reason,
            diagnostics_state=diagnostics_state,
            diagnostics=diagnostics,
        )

        obs_alert_collection_failure.additional_properties = d
        return obs_alert_collection_failure

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

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_regression_review_state import DebugRegressionReviewState, check_debug_regression_review_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRegressionReview")


@_attrs_define
class DebugRegressionReview:
    """Exact workflow-only observation. Opaque version invalidates on every update; state is stored triage metadata, not
    health or effective inbox visibility.

    """

    app_id: UUID
    deployment_id: UUID
    route: str
    version: UUID
    state: DebugRegressionReviewState
    first_detected_at: datetime.datetime
    last_detected_at: datetime.datetime
    acknowledged_at: datetime.datetime | Unset = UNSET
    dismissed_until: datetime.datetime | Unset = UNSET
    resolved_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        route = self.route

        version = str(self.version)

        state: str = self.state

        first_detected_at = self.first_detected_at.isoformat()

        last_detected_at = self.last_detected_at.isoformat()

        acknowledged_at: str | Unset = UNSET
        if not isinstance(self.acknowledged_at, Unset):
            acknowledged_at = self.acknowledged_at.isoformat()

        dismissed_until: str | Unset = UNSET
        if not isinstance(self.dismissed_until, Unset):
            dismissed_until = self.dismissed_until.isoformat()

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "route": route,
                "version": version,
                "state": state,
                "first_detected_at": first_detected_at,
                "last_detected_at": last_detected_at,
            }
        )
        if acknowledged_at is not UNSET:
            field_dict["acknowledged_at"] = acknowledged_at
        if dismissed_until is not UNSET:
            field_dict["dismissed_until"] = dismissed_until
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        route = d.pop("route")

        version = UUID(d.pop("version"))

        state = check_debug_regression_review_state(d.pop("state"))

        first_detected_at = datetime.datetime.fromisoformat(d.pop("first_detected_at"))

        last_detected_at = datetime.datetime.fromisoformat(d.pop("last_detected_at"))

        _acknowledged_at = d.pop("acknowledged_at", UNSET)
        acknowledged_at: datetime.datetime | Unset
        if isinstance(_acknowledged_at, Unset):
            acknowledged_at = UNSET
        else:
            acknowledged_at = datetime.datetime.fromisoformat(_acknowledged_at)

        _dismissed_until = d.pop("dismissed_until", UNSET)
        dismissed_until: datetime.datetime | Unset
        if isinstance(_dismissed_until, Unset):
            dismissed_until = UNSET
        else:
            dismissed_until = datetime.datetime.fromisoformat(_dismissed_until)

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        debug_regression_review = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            route=route,
            version=version,
            state=state,
            first_detected_at=first_detected_at,
            last_detected_at=last_detected_at,
            acknowledged_at=acknowledged_at,
            dismissed_until=dismissed_until,
            resolved_at=resolved_at,
        )

        debug_regression_review.additional_properties = d
        return debug_regression_review

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

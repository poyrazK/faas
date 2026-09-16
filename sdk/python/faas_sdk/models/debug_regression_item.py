from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_regression_item_state import DebugRegressionItemState, check_debug_regression_item_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRegressionItem")


@_attrs_define
class DebugRegressionItem:
    """One regression observation row."""

    deployment_id: UUID
    route: str
    p95_ms: int
    p95_base_ms: int
    affected_count: int
    regression_factor: str
    """Decimal string with up to 2 places, NUMERIC(5,2)."""
    first_detected_at: datetime.datetime
    last_detected_at: datetime.datetime
    state: DebugRegressionItemState
    acknowledged_at: datetime.datetime | Unset = UNSET
    dismissed_until: datetime.datetime | Unset = UNSET
    resolved_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        route = self.route

        p95_ms = self.p95_ms

        p95_base_ms = self.p95_base_ms

        affected_count = self.affected_count

        regression_factor = self.regression_factor

        first_detected_at = self.first_detected_at.isoformat()

        last_detected_at = self.last_detected_at.isoformat()

        state: str = self.state

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
                "deployment_id": deployment_id,
                "route": route,
                "p95_ms": p95_ms,
                "p95_base_ms": p95_base_ms,
                "affected_count": affected_count,
                "regression_factor": regression_factor,
                "first_detected_at": first_detected_at,
                "last_detected_at": last_detected_at,
                "state": state,
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
        deployment_id = UUID(d.pop("deployment_id"))

        route = d.pop("route")

        p95_ms = d.pop("p95_ms")

        p95_base_ms = d.pop("p95_base_ms")

        affected_count = d.pop("affected_count")

        regression_factor = d.pop("regression_factor")

        first_detected_at = datetime.datetime.fromisoformat(d.pop("first_detected_at"))

        last_detected_at = datetime.datetime.fromisoformat(d.pop("last_detected_at"))

        state = check_debug_regression_item_state(d.pop("state"))

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

        debug_regression_item = cls(
            deployment_id=deployment_id,
            route=route,
            p95_ms=p95_ms,
            p95_base_ms=p95_base_ms,
            affected_count=affected_count,
            regression_factor=regression_factor,
            first_detected_at=first_detected_at,
            last_detected_at=last_detected_at,
            state=state,
            acknowledged_at=acknowledged_at,
            dismissed_until=dismissed_until,
            resolved_at=resolved_at,
        )

        debug_regression_item.additional_properties = d
        return debug_regression_item

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

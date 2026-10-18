from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.slo_response_sli import SLOResponseSli, check_slo_response_sli
from ..models.slo_response_window_days import SLOResponseWindowDays, check_slo_response_window_days
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.slo_status import SLOStatus


T = TypeVar("T", bound="SLOResponse")


@_attrs_define
class SLOResponse:
    """One customer-defined SLO (ADR-747)."""

    id: UUID
    name: str
    sli: SLOResponseSli
    objective_pct: float
    window_days: SLOResponseWindowDays
    created_at: datetime.datetime
    latency_threshold_ms: int | Unset = UNSET
    """Threshold for a latency SLO; absent for availability."""
    status: SLOStatus | Unset = UNSET
    """Error-budget position of one SLO, returned by GET /v1/apps/{slug}/slos/{id} (ADR-747)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        sli: str = self.sli

        objective_pct = self.objective_pct

        window_days: int = self.window_days

        created_at = self.created_at.isoformat()

        latency_threshold_ms = self.latency_threshold_ms

        status: dict[str, Any] | Unset = UNSET
        if not isinstance(self.status, Unset):
            status = self.status.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "sli": sli,
                "objective_pct": objective_pct,
                "window_days": window_days,
                "created_at": created_at,
            }
        )
        if latency_threshold_ms is not UNSET:
            field_dict["latency_threshold_ms"] = latency_threshold_ms
        if status is not UNSET:
            field_dict["status"] = status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.slo_status import SLOStatus

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        sli = check_slo_response_sli(d.pop("sli"))

        objective_pct = d.pop("objective_pct")

        window_days = check_slo_response_window_days(d.pop("window_days"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        latency_threshold_ms = d.pop("latency_threshold_ms", UNSET)

        _status = d.pop("status", UNSET)
        status: SLOStatus | Unset
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = SLOStatus.from_dict(_status)

        slo_response = cls(
            id=id,
            name=name,
            sli=sli,
            objective_pct=objective_pct,
            window_days=window_days,
            created_at=created_at,
            latency_threshold_ms=latency_threshold_ms,
            status=status,
        )

        slo_response.additional_properties = d
        return slo_response

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

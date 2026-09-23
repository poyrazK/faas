from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_rollout_handoff_response_action import (
    ServiceRolloutHandoffResponseAction,
    check_service_rollout_handoff_response_action,
)
from ..models.service_rollout_handoff_response_phase import (
    ServiceRolloutHandoffResponsePhase,
    check_service_rollout_handoff_response_phase,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ServiceRolloutHandoffResponse")


@_attrs_define
class ServiceRolloutHandoffResponse:
    """Durable scheduler progress for a zero-downtime service rollout routing and request-drain handoff."""

    action: ServiceRolloutHandoffResponseAction
    phase: ServiceRolloutHandoffResponsePhase
    retry_count: int
    predecessor_deployment_id: str | Unset = UNSET
    generation: int | Unset = UNSET
    expected_gateways: list[str] | Unset = UNSET
    acknowledged_gateways: list[str] | Unset = UNSET
    missing_gateways: list[str] | Unset = UNSET
    last_error: str | Unset = UNSET
    reason: str | Unset = UNSET
    started_at: datetime.datetime | None | Unset = UNSET
    updated_at: datetime.datetime | None | Unset = UNSET
    acknowledged_at: datetime.datetime | None | Unset = UNSET
    completed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        phase: str = self.phase

        retry_count = self.retry_count

        predecessor_deployment_id = self.predecessor_deployment_id

        generation = self.generation

        expected_gateways: list[str] | Unset = UNSET
        if not isinstance(self.expected_gateways, Unset):
            expected_gateways = self.expected_gateways

        acknowledged_gateways: list[str] | Unset = UNSET
        if not isinstance(self.acknowledged_gateways, Unset):
            acknowledged_gateways = self.acknowledged_gateways

        missing_gateways: list[str] | Unset = UNSET
        if not isinstance(self.missing_gateways, Unset):
            missing_gateways = self.missing_gateways

        last_error = self.last_error

        reason = self.reason

        started_at: None | str | Unset
        if isinstance(self.started_at, Unset):
            started_at = UNSET
        elif isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        updated_at: None | str | Unset
        if isinstance(self.updated_at, Unset):
            updated_at = UNSET
        elif isinstance(self.updated_at, datetime.datetime):
            updated_at = self.updated_at.isoformat()
        else:
            updated_at = self.updated_at

        acknowledged_at: None | str | Unset
        if isinstance(self.acknowledged_at, Unset):
            acknowledged_at = UNSET
        elif isinstance(self.acknowledged_at, datetime.datetime):
            acknowledged_at = self.acknowledged_at.isoformat()
        else:
            acknowledged_at = self.acknowledged_at

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
                "phase": phase,
                "retry_count": retry_count,
            }
        )
        if predecessor_deployment_id is not UNSET:
            field_dict["predecessor_deployment_id"] = predecessor_deployment_id
        if generation is not UNSET:
            field_dict["generation"] = generation
        if expected_gateways is not UNSET:
            field_dict["expected_gateways"] = expected_gateways
        if acknowledged_gateways is not UNSET:
            field_dict["acknowledged_gateways"] = acknowledged_gateways
        if missing_gateways is not UNSET:
            field_dict["missing_gateways"] = missing_gateways
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if reason is not UNSET:
            field_dict["reason"] = reason
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if acknowledged_at is not UNSET:
            field_dict["acknowledged_at"] = acknowledged_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_service_rollout_handoff_response_action(d.pop("action"))

        phase = check_service_rollout_handoff_response_phase(d.pop("phase"))

        retry_count = d.pop("retry_count")

        predecessor_deployment_id = d.pop("predecessor_deployment_id", UNSET)

        generation = d.pop("generation", UNSET)

        expected_gateways = cast(list[str], d.pop("expected_gateways", UNSET))

        acknowledged_gateways = cast(list[str], d.pop("acknowledged_gateways", UNSET))

        missing_gateways = cast(list[str], d.pop("missing_gateways", UNSET))

        last_error = d.pop("last_error", UNSET)

        reason = d.pop("reason", UNSET)

        def _parse_started_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = datetime.datetime.fromisoformat(data)

                return started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        started_at = _parse_started_at(d.pop("started_at", UNSET))

        def _parse_updated_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                updated_at_type_0 = datetime.datetime.fromisoformat(data)

                return updated_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        updated_at = _parse_updated_at(d.pop("updated_at", UNSET))

        def _parse_acknowledged_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                acknowledged_at_type_0 = datetime.datetime.fromisoformat(data)

                return acknowledged_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        acknowledged_at = _parse_acknowledged_at(d.pop("acknowledged_at", UNSET))

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        service_rollout_handoff_response = cls(
            action=action,
            phase=phase,
            retry_count=retry_count,
            predecessor_deployment_id=predecessor_deployment_id,
            generation=generation,
            expected_gateways=expected_gateways,
            acknowledged_gateways=acknowledged_gateways,
            missing_gateways=missing_gateways,
            last_error=last_error,
            reason=reason,
            started_at=started_at,
            updated_at=updated_at,
            acknowledged_at=acknowledged_at,
            completed_at=completed_at,
        )

        service_rollout_handoff_response.additional_properties = d
        return service_rollout_handoff_response

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

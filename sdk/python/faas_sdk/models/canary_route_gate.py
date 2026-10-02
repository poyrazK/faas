from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.canary_route_gate_mode import CanaryRouteGateMode, check_canary_route_gate_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="CanaryRouteGate")


@_attrs_define
class CanaryRouteGate:
    """Per-app canary advance gate. Absence is report mode with revision 0. Initial activation, stable rollbacks and aborts
    are outside this gate.

    """

    app_id: UUID
    mode: CanaryRouteGateMode
    revision: int
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        mode: str = self.mode

        revision = self.revision

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "mode": mode,
                "revision": revision,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        mode = check_canary_route_gate_mode(d.pop("mode"))

        revision = d.pop("revision")

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        canary_route_gate = cls(
            app_id=app_id,
            mode=mode,
            revision=revision,
            updated_at=updated_at,
        )

        canary_route_gate.additional_properties = d
        return canary_route_gate

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

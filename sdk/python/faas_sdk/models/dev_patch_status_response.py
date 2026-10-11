from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_patch_status_response_state import DevPatchStatusResponseState, check_dev_patch_status_response_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DevPatchStatusResponse")


@_attrs_define
class DevPatchStatusResponse:
    """Delivery state of one developer live patch (ADR-740)."""

    generation: int
    state: DevPatchStatusResponseState
    created_at: datetime.datetime
    applied_at: datetime.datetime | Unset = UNSET
    """When the first instance acknowledged the patch."""
    apply_ms: int | Unset = UNSET
    """How long the instance took to write the patch and request the restart."""
    error_code: str | Unset = UNSET
    """Bounded guest error code when state is failed, for example apply_failed or restart_failed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generation = self.generation

        state: str = self.state

        created_at = self.created_at.isoformat()

        applied_at: str | Unset = UNSET
        if not isinstance(self.applied_at, Unset):
            applied_at = self.applied_at.isoformat()

        apply_ms = self.apply_ms

        error_code = self.error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generation": generation,
                "state": state,
                "created_at": created_at,
            }
        )
        if applied_at is not UNSET:
            field_dict["applied_at"] = applied_at
        if apply_ms is not UNSET:
            field_dict["apply_ms"] = apply_ms
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        generation = d.pop("generation")

        state = check_dev_patch_status_response_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _applied_at = d.pop("applied_at", UNSET)
        applied_at: datetime.datetime | Unset
        if isinstance(_applied_at, Unset):
            applied_at = UNSET
        else:
            applied_at = datetime.datetime.fromisoformat(_applied_at)

        apply_ms = d.pop("apply_ms", UNSET)

        error_code = d.pop("error_code", UNSET)

        dev_patch_status_response = cls(
            generation=generation,
            state=state,
            created_at=created_at,
            applied_at=applied_at,
            apply_ms=apply_ms,
            error_code=error_code,
        )

        dev_patch_status_response.additional_properties = d
        return dev_patch_status_response

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

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_receiver_invite_status import (
    APISandboxReceiverInviteStatus,
    check_api_sandbox_receiver_invite_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxReceiverInvite")


@_attrs_define
class APISandboxReceiverInvite:
    run_id: UUID
    scenario_revision: UUID
    expires_at: datetime.datetime
    status: APISandboxReceiverInviteStatus
    token: str | Unset = UNSET
    """Returned once when creating the invite."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        scenario_revision = str(self.scenario_revision)

        expires_at = self.expires_at.isoformat()

        status: str = self.status

        token = self.token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "scenario_revision": scenario_revision,
                "expires_at": expires_at,
                "status": status,
            }
        )
        if token is not UNSET:
            field_dict["token"] = token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        scenario_revision = UUID(d.pop("scenario_revision"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        status = check_api_sandbox_receiver_invite_status(d.pop("status"))

        token = d.pop("token", UNSET)

        api_sandbox_receiver_invite = cls(
            run_id=run_id,
            scenario_revision=scenario_revision,
            expires_at=expires_at,
            status=status,
            token=token,
        )

        api_sandbox_receiver_invite.additional_properties = d
        return api_sandbox_receiver_invite

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

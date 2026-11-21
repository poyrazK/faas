from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_risk_event_data import AccountRiskEventData


T = TypeVar("T", bound="AccountRiskEvent")


@_attrs_define
class AccountRiskEvent:
    """An abuse event about the account, one of its apps, deployments or instances."""

    at: datetime.datetime
    kind: str
    actor: str
    subject: str
    data: AccountRiskEventData | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        kind = self.kind

        actor = self.actor

        subject = self.subject

        data: dict[str, Any] | Unset = UNSET
        if not isinstance(self.data, Unset):
            data = self.data.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "kind": kind,
                "actor": actor,
                "subject": subject,
            }
        )
        if data is not UNSET:
            field_dict["data"] = data

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_risk_event_data import AccountRiskEventData

        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = d.pop("kind")

        actor = d.pop("actor")

        subject = d.pop("subject")

        _data = d.pop("data", UNSET)
        data: AccountRiskEventData | Unset
        if isinstance(_data, Unset):
            data = UNSET
        else:
            data = AccountRiskEventData.from_dict(_data)

        account_risk_event = cls(
            at=at,
            kind=kind,
            actor=actor,
            subject=subject,
            data=data,
        )

        account_risk_event.additional_properties = d
        return account_risk_event

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

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagExposure")


@_attrs_define
class FlagExposure:
    """One end user's exposure to a running subject experiment's rule, which SDKs report at most hourly per subject and
    variant. Only the subject's hash is kept.

    """

    flag: str
    rule_id: str
    variant: str
    customer_id: UUID
    subject: str
    at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        flag = self.flag

        rule_id = self.rule_id

        variant = self.variant

        customer_id = str(self.customer_id)

        subject = self.subject

        at: str | Unset = UNSET
        if not isinstance(self.at, Unset):
            at = self.at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "flag": flag,
                "rule_id": rule_id,
                "variant": variant,
                "customer_id": customer_id,
                "subject": subject,
            }
        )
        if at is not UNSET:
            field_dict["at"] = at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        flag = d.pop("flag")

        rule_id = d.pop("rule_id")

        variant = d.pop("variant")

        customer_id = UUID(d.pop("customer_id"))

        subject = d.pop("subject")

        _at = d.pop("at", UNSET)
        at: datetime.datetime | Unset
        if isinstance(_at, Unset):
            at = UNSET
        else:
            at = datetime.datetime.fromisoformat(_at)

        flag_exposure = cls(
            flag=flag,
            rule_id=rule_id,
            variant=variant,
            customer_id=customer_id,
            subject=subject,
            at=at,
        )

        return flag_exposure

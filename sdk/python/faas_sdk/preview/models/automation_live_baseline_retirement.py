from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationLiveBaselineRetirement")


@_attrs_define
class AutomationLiveBaselineRetirement:
    """Immutable attributed receipt recording permanent retirement and release of the baseline evidence pin."""

    retirement_id: UUID
    app_id: UUID
    name: str
    baseline_name: str
    revision: int
    previous_promotion_id: UUID
    actor_account_id: UUID
    retired_at: datetime.datetime
    actor_api_key_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        retirement_id = str(self.retirement_id)

        app_id = str(self.app_id)

        name = self.name

        baseline_name = self.baseline_name

        revision = self.revision

        previous_promotion_id = str(self.previous_promotion_id)

        actor_account_id = str(self.actor_account_id)

        retired_at = self.retired_at.isoformat()

        actor_api_key_id: str | Unset = UNSET
        if not isinstance(self.actor_api_key_id, Unset):
            actor_api_key_id = str(self.actor_api_key_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "retirement_id": retirement_id,
                "app_id": app_id,
                "name": name,
                "baseline_name": baseline_name,
                "revision": revision,
                "previous_promotion_id": previous_promotion_id,
                "actor_account_id": actor_account_id,
                "retired_at": retired_at,
            }
        )
        if actor_api_key_id is not UNSET:
            field_dict["actor_api_key_id"] = actor_api_key_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retirement_id = UUID(d.pop("retirement_id"))

        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        baseline_name = d.pop("baseline_name")

        revision = d.pop("revision")

        previous_promotion_id = UUID(d.pop("previous_promotion_id"))

        actor_account_id = UUID(d.pop("actor_account_id"))

        retired_at = datetime.datetime.fromisoformat(d.pop("retired_at"))

        _actor_api_key_id = d.pop("actor_api_key_id", UNSET)
        actor_api_key_id: UUID | Unset
        if isinstance(_actor_api_key_id, Unset):
            actor_api_key_id = UNSET
        else:
            actor_api_key_id = UUID(_actor_api_key_id)

        automation_live_baseline_retirement = cls(
            retirement_id=retirement_id,
            app_id=app_id,
            name=name,
            baseline_name=baseline_name,
            revision=revision,
            previous_promotion_id=previous_promotion_id,
            actor_account_id=actor_account_id,
            retired_at=retired_at,
            actor_api_key_id=actor_api_key_id,
        )

        return automation_live_baseline_retirement

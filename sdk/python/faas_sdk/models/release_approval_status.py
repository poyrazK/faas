from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_approval_status_state import ReleaseApprovalStatusState, check_release_approval_status_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_approval_decision import ReleaseApprovalDecision


T = TypeVar("T", bound="ReleaseApprovalStatus")


@_attrs_define
class ReleaseApprovalStatus:
    """A release's approval state."""

    deployment_id: UUID
    app: str
    state: ReleaseApprovalStatusState
    approvals: int
    """Approvals that count toward the policy."""
    decisions: list[ReleaseApprovalDecision]
    required_approvals: int | Unset = UNSET
    after_stage: int | Unset = UNSET
    deployed_by: str | Unset = UNSET
    waiting_since: datetime.datetime | Unset = UNSET
    """When the canary entered its waiting stage."""
    expires_at: datetime.datetime | Unset = UNSET
    """When a release still waiting is aborted."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        app = self.app

        state: str = self.state

        approvals = self.approvals

        decisions = []
        for decisions_item_data in self.decisions:
            decisions_item = decisions_item_data.to_dict()
            decisions.append(decisions_item)

        required_approvals = self.required_approvals

        after_stage = self.after_stage

        deployed_by = self.deployed_by

        waiting_since: str | Unset = UNSET
        if not isinstance(self.waiting_since, Unset):
            waiting_since = self.waiting_since.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "app": app,
                "state": state,
                "approvals": approvals,
                "decisions": decisions,
            }
        )
        if required_approvals is not UNSET:
            field_dict["required_approvals"] = required_approvals
        if after_stage is not UNSET:
            field_dict["after_stage"] = after_stage
        if deployed_by is not UNSET:
            field_dict["deployed_by"] = deployed_by
        if waiting_since is not UNSET:
            field_dict["waiting_since"] = waiting_since
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_approval_decision import ReleaseApprovalDecision

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        app = d.pop("app")

        state = check_release_approval_status_state(d.pop("state"))

        approvals = d.pop("approvals")

        decisions = []
        _decisions = d.pop("decisions")
        for decisions_item_data in _decisions:
            decisions_item = ReleaseApprovalDecision.from_dict(decisions_item_data)

            decisions.append(decisions_item)

        required_approvals = d.pop("required_approvals", UNSET)

        after_stage = d.pop("after_stage", UNSET)

        deployed_by = d.pop("deployed_by", UNSET)

        _waiting_since = d.pop("waiting_since", UNSET)
        waiting_since: datetime.datetime | Unset
        if isinstance(_waiting_since, Unset):
            waiting_since = UNSET
        else:
            waiting_since = datetime.datetime.fromisoformat(_waiting_since)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        release_approval_status = cls(
            deployment_id=deployment_id,
            app=app,
            state=state,
            approvals=approvals,
            decisions=decisions,
            required_approvals=required_approvals,
            after_stage=after_stage,
            deployed_by=deployed_by,
            waiting_since=waiting_since,
            expires_at=expires_at,
        )

        release_approval_status.additional_properties = d
        return release_approval_status

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

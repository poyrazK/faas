from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_audit_response_kind import DeploymentAuditResponseKind, check_deployment_audit_response_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentAuditResponse")


@_attrs_define
class DeploymentAuditResponse:
    """One row of the deployment_audit timeline (issue #976 /
    ADR-122 / SAFE-RELEASES-E.2 + production-leveling Stream
    A). Data is the verbatim jsonb payload at emit time;
    kind-specific shape — DeployTrafficChanged carries
    {from_percent, to_percent, actor_kind}, DeployRolledBack
    carries {target_deployment_id, reason}.

    """

    at: datetime.datetime
    """RFC3339Nano UTC timestamp at which the row was emitted. Sequence-pointed — sorting by (at DESC) gives the
    canonical timeline order."""
    kind: DeploymentAuditResponseKind
    """Closed-set event kind (migrations/00477 enforces the same CHECK on the deployments_audit table)."""
    actor: str
    """Service-account UUID or operator CLI sentinel (`operator:cli:recover_rollout`, `meterd:safedeploy`,
    `meterd:canary_progression`). Operators identify who did what from this column."""
    data: Any | Unset = UNSET
    """Verbatim jsonb payload at emit time (kind-specific shape — see description)."""
    account_id: str | Unset = UNSET
    """Owning account UUID (cross-tenant IDOR posture)."""
    alert_rule_id: UUID | Unset = UNSET
    """SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122): when the audit row was stamped by an alert-rule-fired action
    (e.g. auto-rollback via meterd ActionDispatcher), this carries the alert_rules.id UUID. nil for non-rule-
    triggered rows. Wire-additive per ADR-016 — null for all pre-PR-D rows."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        kind: str = self.kind

        actor = self.actor

        data = self.data

        account_id = self.account_id

        alert_rule_id: str | Unset = UNSET
        if not isinstance(self.alert_rule_id, Unset):
            alert_rule_id = str(self.alert_rule_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "kind": kind,
                "actor": actor,
            }
        )
        if data is not UNSET:
            field_dict["data"] = data
        if account_id is not UNSET:
            field_dict["account_id"] = account_id
        if alert_rule_id is not UNSET:
            field_dict["alert_rule_id"] = alert_rule_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = check_deployment_audit_response_kind(d.pop("kind"))

        actor = d.pop("actor")

        data = d.pop("data", UNSET)

        account_id = d.pop("account_id", UNSET)

        _alert_rule_id = d.pop("alert_rule_id", UNSET)
        alert_rule_id: UUID | Unset
        if isinstance(_alert_rule_id, Unset):
            alert_rule_id = UNSET
        else:
            alert_rule_id = UUID(_alert_rule_id)

        deployment_audit_response = cls(
            at=at,
            kind=kind,
            actor=actor,
            data=data,
            account_id=account_id,
            alert_rule_id=alert_rule_id,
        )

        deployment_audit_response.additional_properties = d
        return deployment_audit_response

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

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RollbackRequest")


@_attrs_define
class RollbackRequest:
    """Body for POST /v1/apps/{slug}/rollback (SAFE-RELEASES-G, issue #976). All fields optional. Without a body the
    handler falls back to rolling back to the most-recent superseded deployment (pre-#976 behaviour). With
    `target_deployment_id` set, the handler validates that the named deployment belongs to this app and is superseded or
    live with zero traffic.

    """

    target_deployment_id: UUID | Unset = UNSET
    """The UUID or vN of the deployment to promote back to 'live'. Must belong to the same app as the URL slug, and
    be superseded or live with zero traffic. Nil/empty falls back to the most-recent superseded deployment (legacy
    behaviour)."""
    alert_rule_id: UUID | Unset = UNSET
    """SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122): when set, the handler stamps the deployment_audit row's
    alert_rule_id column with this UUID so an operator can cross-link the audit timeline back to
    /dashboard/alerts/{id}. Wire-additive per ADR-016; the field is ignored when nil/empty. Only privileged in-
    process callers (meterd ActionDispatcher) set this; the API does not enforce role because the endpoint already
    requires MFA + ScopesDeployWrite."""
    expected_current_deployment_id: UUID | Unset = UNSET
    """Exact current deployment UUID or vN. Together with an explicit target starts the checked asynchronous
    workflow and returns rollback_operation. Rejects a changed current deployment instead of selecting another one.
   """
    reason: str | Unset = UNSET
    """One-line reason for an exact checked rollback. Cannot be combined with alert_rule_id."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target_deployment_id: str | Unset = UNSET
        if not isinstance(self.target_deployment_id, Unset):
            target_deployment_id = str(self.target_deployment_id)

        alert_rule_id: str | Unset = UNSET
        if not isinstance(self.alert_rule_id, Unset):
            alert_rule_id = str(self.alert_rule_id)

        expected_current_deployment_id: str | Unset = UNSET
        if not isinstance(self.expected_current_deployment_id, Unset):
            expected_current_deployment_id = str(self.expected_current_deployment_id)

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if alert_rule_id is not UNSET:
            field_dict["alert_rule_id"] = alert_rule_id
        if expected_current_deployment_id is not UNSET:
            field_dict["expected_current_deployment_id"] = expected_current_deployment_id
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _target_deployment_id = d.pop("target_deployment_id", UNSET)
        target_deployment_id: UUID | Unset
        if isinstance(_target_deployment_id, Unset):
            target_deployment_id = UNSET
        else:
            target_deployment_id = UUID(_target_deployment_id)

        _alert_rule_id = d.pop("alert_rule_id", UNSET)
        alert_rule_id: UUID | Unset
        if isinstance(_alert_rule_id, Unset):
            alert_rule_id = UNSET
        else:
            alert_rule_id = UUID(_alert_rule_id)

        _expected_current_deployment_id = d.pop("expected_current_deployment_id", UNSET)
        expected_current_deployment_id: UUID | Unset
        if isinstance(_expected_current_deployment_id, Unset):
            expected_current_deployment_id = UNSET
        else:
            expected_current_deployment_id = UUID(_expected_current_deployment_id)

        reason = d.pop("reason", UNSET)

        rollback_request = cls(
            target_deployment_id=target_deployment_id,
            alert_rule_id=alert_rule_id,
            expected_current_deployment_id=expected_current_deployment_id,
            reason=reason,
        )

        rollback_request.additional_properties = d
        return rollback_request

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

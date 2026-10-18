from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_incident_attention_reminder_policy_source import (
    MCPReadinessIncidentAttentionReminderPolicySource,
    check_mcp_readiness_incident_attention_reminder_policy_source,
)
from ..models.mcp_readiness_incident_attention_state import (
    MCPReadinessIncidentAttentionState,
    check_mcp_readiness_incident_attention_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_acknowledgement import MCPReadinessAcknowledgement
    from ..models.mcp_readiness_attention_receipt import MCPReadinessAttentionReceipt
    from ..models.mcp_readiness_pager_duty_acknowledgement_hold import MCPReadinessPagerDutyAcknowledgementHold
    from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy


T = TypeVar("T", bound="MCPReadinessIncidentAttention")


@_attrs_define
class MCPReadinessIncidentAttention:
    """Incident acknowledgement and durable reminder schedule. Version changes on decisions and reminder creation, not
    routine evidence updates.

    """

    incident_id: UUID
    rule_id: UUID
    rule_name: str
    rule_enabled: bool
    opened_at: datetime.datetime
    version: int
    state: MCPReadinessIncidentAttentionState
    reminder_policy: MCPReadinessReminderPolicy
    """Per-incident lifetime creation budget. Both zero disables; otherwise interval is 60–86400 seconds and budget
    is 1–10. Reconfiguration never resets the count."""
    reminders_created: int
    provider_acknowledgements: list[MCPReadinessPagerDutyAcknowledgementHold] | Unset = UNSET
    """Active tracked PagerDuty acknowledgements in primary then fallback order. Every remaining acknowledgement
    prevents automatic reminder resumption."""
    reminder_policy_source: MCPReadinessIncidentAttentionReminderPolicySource | Unset = UNSET
    """Whether the current incident policy was inherited or explicitly overridden; legacy denotes unavailable pre-
    upgrade provenance."""
    reminder_defaults_version: int | Unset = UNSET
    """Rule defaults version captured at incident opening; retained after an incident override and absent for older
    incidents."""
    mcp_readiness_scope: str | Unset = UNSET
    """Incident environment; omission covers every environment."""
    resolved_at: datetime.datetime | Unset = UNSET
    acknowledgement: MCPReadinessAcknowledgement | Unset = UNSET
    """Retained acknowledgement metadata independent of readiness state."""
    next_reminder_at: datetime.datetime | Unset = UNSET
    """Earliest eligibility subject to fresh evidence and queue state."""
    last_observed_at: datetime.datetime | Unset = UNSET
    last_unhealthy_at: datetime.datetime | Unset = UNSET
    """Latest matching unhealthy evidence; omitted after healthy or coverage-changed observations."""
    last_action: MCPReadinessAttentionReceipt | Unset = UNSET
    """Immutable attention decision retained transactionally with the incident state change."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        incident_id = str(self.incident_id)

        rule_id = str(self.rule_id)

        rule_name = self.rule_name

        rule_enabled = self.rule_enabled

        opened_at = self.opened_at.isoformat()

        version = self.version

        state: str = self.state

        reminder_policy = self.reminder_policy.to_dict()

        reminders_created = self.reminders_created

        provider_acknowledgements: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.provider_acknowledgements, Unset):
            provider_acknowledgements = []
            for provider_acknowledgements_item_data in self.provider_acknowledgements:
                provider_acknowledgements_item = provider_acknowledgements_item_data.to_dict()
                provider_acknowledgements.append(provider_acknowledgements_item)

        reminder_policy_source: str | Unset = UNSET
        if not isinstance(self.reminder_policy_source, Unset):
            reminder_policy_source = self.reminder_policy_source

        reminder_defaults_version = self.reminder_defaults_version

        mcp_readiness_scope = self.mcp_readiness_scope

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        acknowledgement: dict[str, Any] | Unset = UNSET
        if not isinstance(self.acknowledgement, Unset):
            acknowledgement = self.acknowledgement.to_dict()

        next_reminder_at: str | Unset = UNSET
        if not isinstance(self.next_reminder_at, Unset):
            next_reminder_at = self.next_reminder_at.isoformat()

        last_observed_at: str | Unset = UNSET
        if not isinstance(self.last_observed_at, Unset):
            last_observed_at = self.last_observed_at.isoformat()

        last_unhealthy_at: str | Unset = UNSET
        if not isinstance(self.last_unhealthy_at, Unset):
            last_unhealthy_at = self.last_unhealthy_at.isoformat()

        last_action: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_action, Unset):
            last_action = self.last_action.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "incident_id": incident_id,
                "rule_id": rule_id,
                "rule_name": rule_name,
                "rule_enabled": rule_enabled,
                "opened_at": opened_at,
                "version": version,
                "state": state,
                "reminder_policy": reminder_policy,
                "reminders_created": reminders_created,
            }
        )
        if provider_acknowledgements is not UNSET:
            field_dict["provider_acknowledgements"] = provider_acknowledgements
        if reminder_policy_source is not UNSET:
            field_dict["reminder_policy_source"] = reminder_policy_source
        if reminder_defaults_version is not UNSET:
            field_dict["reminder_defaults_version"] = reminder_defaults_version
        if mcp_readiness_scope is not UNSET:
            field_dict["mcp_readiness_scope"] = mcp_readiness_scope
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if acknowledgement is not UNSET:
            field_dict["acknowledgement"] = acknowledgement
        if next_reminder_at is not UNSET:
            field_dict["next_reminder_at"] = next_reminder_at
        if last_observed_at is not UNSET:
            field_dict["last_observed_at"] = last_observed_at
        if last_unhealthy_at is not UNSET:
            field_dict["last_unhealthy_at"] = last_unhealthy_at
        if last_action is not UNSET:
            field_dict["last_action"] = last_action

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_acknowledgement import MCPReadinessAcknowledgement
        from ..models.mcp_readiness_attention_receipt import MCPReadinessAttentionReceipt
        from ..models.mcp_readiness_pager_duty_acknowledgement_hold import MCPReadinessPagerDutyAcknowledgementHold
        from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy

        d = dict(src_dict)
        incident_id = UUID(d.pop("incident_id"))

        rule_id = UUID(d.pop("rule_id"))

        rule_name = d.pop("rule_name")

        rule_enabled = d.pop("rule_enabled")

        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        version = d.pop("version")

        state = check_mcp_readiness_incident_attention_state(d.pop("state"))

        reminder_policy = MCPReadinessReminderPolicy.from_dict(d.pop("reminder_policy"))

        reminders_created = d.pop("reminders_created")

        _provider_acknowledgements = d.pop("provider_acknowledgements", UNSET)
        provider_acknowledgements: list[MCPReadinessPagerDutyAcknowledgementHold] | Unset = UNSET
        if _provider_acknowledgements is not UNSET:
            provider_acknowledgements = []
            for provider_acknowledgements_item_data in _provider_acknowledgements:
                provider_acknowledgements_item = MCPReadinessPagerDutyAcknowledgementHold.from_dict(
                    provider_acknowledgements_item_data
                )

                provider_acknowledgements.append(provider_acknowledgements_item)

        _reminder_policy_source = d.pop("reminder_policy_source", UNSET)
        reminder_policy_source: MCPReadinessIncidentAttentionReminderPolicySource | Unset
        if isinstance(_reminder_policy_source, Unset):
            reminder_policy_source = UNSET
        else:
            reminder_policy_source = check_mcp_readiness_incident_attention_reminder_policy_source(
                _reminder_policy_source
            )

        reminder_defaults_version = d.pop("reminder_defaults_version", UNSET)

        mcp_readiness_scope = d.pop("mcp_readiness_scope", UNSET)

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        _acknowledgement = d.pop("acknowledgement", UNSET)
        acknowledgement: MCPReadinessAcknowledgement | Unset
        if isinstance(_acknowledgement, Unset):
            acknowledgement = UNSET
        else:
            acknowledgement = MCPReadinessAcknowledgement.from_dict(_acknowledgement)

        _next_reminder_at = d.pop("next_reminder_at", UNSET)
        next_reminder_at: datetime.datetime | Unset
        if isinstance(_next_reminder_at, Unset):
            next_reminder_at = UNSET
        else:
            next_reminder_at = datetime.datetime.fromisoformat(_next_reminder_at)

        _last_observed_at = d.pop("last_observed_at", UNSET)
        last_observed_at: datetime.datetime | Unset
        if isinstance(_last_observed_at, Unset):
            last_observed_at = UNSET
        else:
            last_observed_at = datetime.datetime.fromisoformat(_last_observed_at)

        _last_unhealthy_at = d.pop("last_unhealthy_at", UNSET)
        last_unhealthy_at: datetime.datetime | Unset
        if isinstance(_last_unhealthy_at, Unset):
            last_unhealthy_at = UNSET
        else:
            last_unhealthy_at = datetime.datetime.fromisoformat(_last_unhealthy_at)

        _last_action = d.pop("last_action", UNSET)
        last_action: MCPReadinessAttentionReceipt | Unset
        if isinstance(_last_action, Unset):
            last_action = UNSET
        else:
            last_action = MCPReadinessAttentionReceipt.from_dict(_last_action)

        mcp_readiness_incident_attention = cls(
            incident_id=incident_id,
            rule_id=rule_id,
            rule_name=rule_name,
            rule_enabled=rule_enabled,
            opened_at=opened_at,
            version=version,
            state=state,
            reminder_policy=reminder_policy,
            reminders_created=reminders_created,
            provider_acknowledgements=provider_acknowledgements,
            reminder_policy_source=reminder_policy_source,
            reminder_defaults_version=reminder_defaults_version,
            mcp_readiness_scope=mcp_readiness_scope,
            resolved_at=resolved_at,
            acknowledgement=acknowledgement,
            next_reminder_at=next_reminder_at,
            last_observed_at=last_observed_at,
            last_unhealthy_at=last_unhealthy_at,
            last_action=last_action,
        )

        mcp_readiness_incident_attention.additional_properties = d
        return mcp_readiness_incident_attention

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

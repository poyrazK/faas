from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_observed_incident_outcome import AlertObservedIncidentOutcome, check_alert_observed_incident_outcome
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_observed_incident_opening_evidence import AlertObservedIncidentOpeningEvidence


T = TypeVar("T", bound="AlertObservedIncident")


@_attrs_define
class AlertObservedIncident:
    """An incident observed in live traffic or reconstructed during replay."""

    opened_at: datetime.datetime
    eligible_notifications: int
    """ Notification opportunities for this incident under the rule's cooldown. """
    last_eligible_at: datetime.datetime
    opening_evidence: AlertObservedIncidentOpeningEvidence | Unset = UNSET
    """ Values captured at the first breach of a live observed incident. """
    environment_scope: str | Unset = UNSET
    """ Environment of this observation when scoped. """
    ended_at: datetime.datetime | Unset = UNSET
    outcome: AlertObservedIncidentOutcome | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        opened_at = self.opened_at.isoformat()

        eligible_notifications = self.eligible_notifications

        last_eligible_at = self.last_eligible_at.isoformat()

        opening_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.opening_evidence, Unset):
            opening_evidence = self.opening_evidence.to_dict()

        environment_scope = self.environment_scope

        ended_at: str | Unset = UNSET
        if not isinstance(self.ended_at, Unset):
            ended_at = self.ended_at.isoformat()

        outcome: str | Unset = UNSET
        if not isinstance(self.outcome, Unset):
            outcome = self.outcome

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "opened_at": opened_at,
                "eligible_notifications": eligible_notifications,
                "last_eligible_at": last_eligible_at,
            }
        )
        if opening_evidence is not UNSET:
            field_dict["opening_evidence"] = opening_evidence
        if environment_scope is not UNSET:
            field_dict["environment_scope"] = environment_scope
        if ended_at is not UNSET:
            field_dict["ended_at"] = ended_at
        if outcome is not UNSET:
            field_dict["outcome"] = outcome

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_observed_incident_opening_evidence import AlertObservedIncidentOpeningEvidence

        d = dict(src_dict)
        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        eligible_notifications = d.pop("eligible_notifications")

        last_eligible_at = datetime.datetime.fromisoformat(d.pop("last_eligible_at"))

        _opening_evidence = d.pop("opening_evidence", UNSET)
        opening_evidence: AlertObservedIncidentOpeningEvidence | Unset
        if isinstance(_opening_evidence, Unset):
            opening_evidence = UNSET
        else:
            opening_evidence = AlertObservedIncidentOpeningEvidence.from_dict(_opening_evidence)

        environment_scope = d.pop("environment_scope", UNSET)

        _ended_at = d.pop("ended_at", UNSET)
        ended_at: datetime.datetime | Unset
        if isinstance(_ended_at, Unset):
            ended_at = UNSET
        else:
            ended_at = datetime.datetime.fromisoformat(_ended_at)

        _outcome = d.pop("outcome", UNSET)
        outcome: AlertObservedIncidentOutcome | Unset
        if isinstance(_outcome, Unset):
            outcome = UNSET
        else:
            outcome = check_alert_observed_incident_outcome(_outcome)

        alert_observed_incident = cls(
            opened_at=opened_at,
            eligible_notifications=eligible_notifications,
            last_eligible_at=last_eligible_at,
            opening_evidence=opening_evidence,
            environment_scope=environment_scope,
            ended_at=ended_at,
            outcome=outcome,
        )

        alert_observed_incident.additional_properties = d
        return alert_observed_incident

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

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_rule_health_item_issues_item import (
    ObsRuleHealthItemIssuesItem,
    check_obs_rule_health_item_issues_item,
)
from ..models.obs_rule_health_item_kind import ObsRuleHealthItemKind, check_obs_rule_health_item_kind
from ..models.obs_rule_health_item_reported_state import (
    ObsRuleHealthItemReportedState,
    check_obs_rule_health_item_reported_state,
)
from ..models.obs_rule_health_item_state import ObsRuleHealthItemState, check_obs_rule_health_item_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuleHealthItem")


@_attrs_define
class ObsRuleHealthItem:
    """Safe identity and closed diagnostics for a loaded rule or an invalid empty group. Counts are null unless healthy;
    age is null for absent or future evaluation clocks within the accepted skew tolerance.

    """

    kind: ObsRuleHealthItemKind
    id: str
    group_id: str
    state: ObsRuleHealthItemState
    issues: list[ObsRuleHealthItemIssuesItem]
    reported_state: ObsRuleHealthItemReportedState
    evaluation_age_seconds: int | None
    has_evaluation_error: bool
    pending: int | None
    firing: int | None
    group_name: str | Unset = UNSET
    file_name: str | Unset = UNSET
    rule_name: str | Unset = UNSET
    last_evaluated_at: datetime.datetime | Unset = UNSET
    runbook_url: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        id = self.id

        group_id = self.group_id

        state: str = self.state

        issues = []
        for issues_item_data in self.issues:
            issues_item: str = issues_item_data
            issues.append(issues_item)

        reported_state: str = self.reported_state

        evaluation_age_seconds: int | None
        evaluation_age_seconds = self.evaluation_age_seconds

        has_evaluation_error = self.has_evaluation_error

        pending: int | None
        pending = self.pending

        firing: int | None
        firing = self.firing

        group_name = self.group_name

        file_name = self.file_name

        rule_name = self.rule_name

        last_evaluated_at: str | Unset = UNSET
        if not isinstance(self.last_evaluated_at, Unset):
            last_evaluated_at = self.last_evaluated_at.isoformat()

        runbook_url = self.runbook_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "id": id,
                "group_id": group_id,
                "state": state,
                "issues": issues,
                "reported_state": reported_state,
                "evaluation_age_seconds": evaluation_age_seconds,
                "has_evaluation_error": has_evaluation_error,
                "pending": pending,
                "firing": firing,
            }
        )
        if group_name is not UNSET:
            field_dict["group_name"] = group_name
        if file_name is not UNSET:
            field_dict["file_name"] = file_name
        if rule_name is not UNSET:
            field_dict["rule_name"] = rule_name
        if last_evaluated_at is not UNSET:
            field_dict["last_evaluated_at"] = last_evaluated_at
        if runbook_url is not UNSET:
            field_dict["runbook_url"] = runbook_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_obs_rule_health_item_kind(d.pop("kind"))

        id = d.pop("id")

        group_id = d.pop("group_id")

        state = check_obs_rule_health_item_state(d.pop("state"))

        issues = []
        _issues = d.pop("issues")
        for issues_item_data in _issues:
            issues_item = check_obs_rule_health_item_issues_item(issues_item_data)

            issues.append(issues_item)

        reported_state = check_obs_rule_health_item_reported_state(d.pop("reported_state"))

        def _parse_evaluation_age_seconds(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        evaluation_age_seconds = _parse_evaluation_age_seconds(d.pop("evaluation_age_seconds"))

        has_evaluation_error = d.pop("has_evaluation_error")

        def _parse_pending(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        pending = _parse_pending(d.pop("pending"))

        def _parse_firing(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        firing = _parse_firing(d.pop("firing"))

        group_name = d.pop("group_name", UNSET)

        file_name = d.pop("file_name", UNSET)

        rule_name = d.pop("rule_name", UNSET)

        _last_evaluated_at = d.pop("last_evaluated_at", UNSET)
        last_evaluated_at: datetime.datetime | Unset
        if isinstance(_last_evaluated_at, Unset):
            last_evaluated_at = UNSET
        else:
            last_evaluated_at = datetime.datetime.fromisoformat(_last_evaluated_at)

        runbook_url = d.pop("runbook_url", UNSET)

        obs_rule_health_item = cls(
            kind=kind,
            id=id,
            group_id=group_id,
            state=state,
            issues=issues,
            reported_state=reported_state,
            evaluation_age_seconds=evaluation_age_seconds,
            has_evaluation_error=has_evaluation_error,
            pending=pending,
            firing=firing,
            group_name=group_name,
            file_name=file_name,
            rule_name=rule_name,
            last_evaluated_at=last_evaluated_at,
            runbook_url=runbook_url,
        )

        obs_rule_health_item.additional_properties = d
        return obs_rule_health_item

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

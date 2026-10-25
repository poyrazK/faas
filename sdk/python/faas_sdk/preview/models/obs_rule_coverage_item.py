from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_rule_coverage_item_changed_fields_item import (
    ObsRuleCoverageItemChangedFieldsItem,
    check_obs_rule_coverage_item_changed_fields_item,
)
from ..models.obs_rule_coverage_item_reason import ObsRuleCoverageItemReason, check_obs_rule_coverage_item_reason
from ..models.obs_rule_coverage_item_state import ObsRuleCoverageItemState, check_obs_rule_coverage_item_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuleCoverageItem")


@_attrs_define
class ObsRuleCoverageItem:
    """One expected identity with safe catalog metadata and closed comparison evidence; these IDs are independent of
    historical episode rule keys.

    """

    id: str
    file_name: str
    group_name: str
    rule_name: str
    state: ObsRuleCoverageItemState
    expected_fingerprint: str
    observed_candidates: int
    changed_fields: list[ObsRuleCoverageItemChangedFieldsItem]
    """Changed fields in expression then for then keep_firing_for then labels order; empty unless comparison is
    known changed."""
    reason: ObsRuleCoverageItemReason | Unset = UNSET
    observed_fingerprint: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        file_name = self.file_name

        group_name = self.group_name

        rule_name = self.rule_name

        state: str = self.state

        expected_fingerprint = self.expected_fingerprint

        observed_candidates = self.observed_candidates

        changed_fields = []
        for changed_fields_item_data in self.changed_fields:
            changed_fields_item: str = changed_fields_item_data
            changed_fields.append(changed_fields_item)

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        observed_fingerprint = self.observed_fingerprint

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "file_name": file_name,
                "group_name": group_name,
                "rule_name": rule_name,
                "state": state,
                "expected_fingerprint": expected_fingerprint,
                "observed_candidates": observed_candidates,
                "changed_fields": changed_fields,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if observed_fingerprint is not UNSET:
            field_dict["observed_fingerprint"] = observed_fingerprint

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        file_name = d.pop("file_name")

        group_name = d.pop("group_name")

        rule_name = d.pop("rule_name")

        state = check_obs_rule_coverage_item_state(d.pop("state"))

        expected_fingerprint = d.pop("expected_fingerprint")

        observed_candidates = d.pop("observed_candidates")

        changed_fields = []
        _changed_fields = d.pop("changed_fields")
        for changed_fields_item_data in _changed_fields:
            changed_fields_item = check_obs_rule_coverage_item_changed_fields_item(changed_fields_item_data)

            changed_fields.append(changed_fields_item)

        _reason = d.pop("reason", UNSET)
        reason: ObsRuleCoverageItemReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_rule_coverage_item_reason(_reason)

        observed_fingerprint = d.pop("observed_fingerprint", UNSET)

        obs_rule_coverage_item = cls(
            id=id,
            file_name=file_name,
            group_name=group_name,
            rule_name=rule_name,
            state=state,
            expected_fingerprint=expected_fingerprint,
            observed_candidates=observed_candidates,
            changed_fields=changed_fields,
            reason=reason,
            observed_fingerprint=observed_fingerprint,
        )

        obs_rule_coverage_item.additional_properties = d
        return obs_rule_coverage_item

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

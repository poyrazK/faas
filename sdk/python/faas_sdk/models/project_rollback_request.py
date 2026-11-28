from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackRequest")


@_attrs_define
class ProjectRollbackRequest:
    """Select production revisions to restore and configure rollback admission and verification."""

    all_: bool | Unset = False
    acknowledge_releases: bool | Unset = False
    verify: bool | Unset = False
    """ Check configured HTTP health probes after each cutover before continuing. """
    verify_before_cutover: bool | Unset = False
    """ Verify configured HTTP probes of all prepared revisions at zero traffic before the first cutover. Requires
    verify. """
    stabilize_for_seconds: int | Unset = 0
    """ Minimum healthy probe duration in seconds after each cutover. Nonzero values require verify. """
    verify_live_traffic: bool | Unset = False
    """ Verify configured production 5xx budgets after cutover before advancing. Requires verify. """
    undo_on_health_failure: bool | Unset = False
    """ Automatically undo switched services on a confirmed health probe 5xx. Requires verify. """
    undo_on_live_traffic_failure: bool | Unset = False
    """ Automatically undo switched services on a reconfirmed production 5xx budget breach. Requires
    verify_live_traffic. """
    request_id: UUID | Unset = UNSET
    """ Stable start request UUID. Repeat with the same options to read the accepted operation. """
    expected_plan_hash: str | Unset = UNSET
    """ Plan hash from a runnable preview with the same rollback options. """
    to_deploy: UUID | Unset = UNSET
    """ Recorded production deploy group to restore. Omit all when selecting a deploy group. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        all_ = self.all_

        acknowledge_releases = self.acknowledge_releases

        verify = self.verify

        verify_before_cutover = self.verify_before_cutover

        stabilize_for_seconds = self.stabilize_for_seconds

        verify_live_traffic = self.verify_live_traffic

        undo_on_health_failure = self.undo_on_health_failure

        undo_on_live_traffic_failure = self.undo_on_live_traffic_failure

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        expected_plan_hash = self.expected_plan_hash

        to_deploy: str | Unset = UNSET
        if not isinstance(self.to_deploy, Unset):
            to_deploy = str(self.to_deploy)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if all_ is not UNSET:
            field_dict["all"] = all_
        if acknowledge_releases is not UNSET:
            field_dict["acknowledge_releases"] = acknowledge_releases
        if verify is not UNSET:
            field_dict["verify"] = verify
        if verify_before_cutover is not UNSET:
            field_dict["verify_before_cutover"] = verify_before_cutover
        if stabilize_for_seconds is not UNSET:
            field_dict["stabilize_for_seconds"] = stabilize_for_seconds
        if verify_live_traffic is not UNSET:
            field_dict["verify_live_traffic"] = verify_live_traffic
        if undo_on_health_failure is not UNSET:
            field_dict["undo_on_health_failure"] = undo_on_health_failure
        if undo_on_live_traffic_failure is not UNSET:
            field_dict["undo_on_live_traffic_failure"] = undo_on_live_traffic_failure
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if expected_plan_hash is not UNSET:
            field_dict["expected_plan_hash"] = expected_plan_hash
        if to_deploy is not UNSET:
            field_dict["to_deploy"] = to_deploy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        all_ = d.pop("all", UNSET)

        acknowledge_releases = d.pop("acknowledge_releases", UNSET)

        verify = d.pop("verify", UNSET)

        verify_before_cutover = d.pop("verify_before_cutover", UNSET)

        stabilize_for_seconds = d.pop("stabilize_for_seconds", UNSET)

        verify_live_traffic = d.pop("verify_live_traffic", UNSET)

        undo_on_health_failure = d.pop("undo_on_health_failure", UNSET)

        undo_on_live_traffic_failure = d.pop("undo_on_live_traffic_failure", UNSET)

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        expected_plan_hash = d.pop("expected_plan_hash", UNSET)

        _to_deploy = d.pop("to_deploy", UNSET)
        to_deploy: UUID | Unset
        if isinstance(_to_deploy, Unset):
            to_deploy = UNSET
        else:
            to_deploy = UUID(_to_deploy)

        project_rollback_request = cls(
            all_=all_,
            acknowledge_releases=acknowledge_releases,
            verify=verify,
            verify_before_cutover=verify_before_cutover,
            stabilize_for_seconds=stabilize_for_seconds,
            verify_live_traffic=verify_live_traffic,
            undo_on_health_failure=undo_on_health_failure,
            undo_on_live_traffic_failure=undo_on_live_traffic_failure,
            request_id=request_id,
            expected_plan_hash=expected_plan_hash,
            to_deploy=to_deploy,
        )

        project_rollback_request.additional_properties = d
        return project_rollback_request

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

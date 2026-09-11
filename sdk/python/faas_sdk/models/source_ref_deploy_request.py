from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.source_ref_deploy_request_format import (
    SourceRefDeployRequestFormat,
    check_source_ref_deploy_request_format,
)
from ..models.source_ref_deploy_request_tag import SourceRefDeployRequestTag, check_source_ref_deploy_request_tag
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.canary_preset_spec import CanaryPresetSpec


T = TypeVar("T", bound="SourceRefDeployRequest")


@_attrs_define
class SourceRefDeployRequest:
    """JSON body for POST /v1/apps/{slug}/deployments/source-ref
    (DEPLOY-PROV-4 / ADR-092, issue #739). The headless CI deploy
    path: `repo` resolves to an install-token-bound fetch, `ref` is
    the customer's chosen input (branch / tag / SHA — server
    resolves to a 40-char SHA before stamping the deployment row).

    """

    repo: str
    """GitHub owner/name slug, e.g. `onebox-faas/hello`."""
    ref: str
    """Commit ref — 40-char SHA, branch, or tag. api.github.com
    /repos/<repo>/commits/<ref> resolves branch / tag inputs
    to a pinned SHA before the tarball fetch starts. The wire
    shape pins to the resolved 40-char SHA (server override;
    caller's `ref` is preserved on the `deploy.source_ref`
    audit row for traceability).
    """
    format_: SourceRefDeployRequestFormat | Unset = "tarball"
    """Forward-compat field. PR-A only supports `tarball`."""
    reason: str | Unset = UNSET
    """Free-form operator note (≤280 chars). Example: 'Emergency rollback after payment provider incident'."""
    tag: SourceRefDeployRequestTag | Unset = UNSET
    """Closed-set annotation tag for grouping/filtering."""
    deployed_by: str | Unset = UNSET
    """Human-readable actor label. CLI auto-captures from `git config user.name`; githubd stamps pusher.name; the
    GitHub Action defaults to ${{ github.actor }}."""
    pr_number: int | Unset = UNSET
    """Pull-request number when the wire offers it (githubd pull_request.number; Action ${{
    github.event.pull_request.number }}). NULL for push-to-main with no inferred PR."""
    traffic_percent: int | None | Unset = UNSET
    """Explicit initial traffic weight for this source-ref deployment. Omitted uses the normal stable rollout; zero
    stages a dark live revision."""
    canary: CanaryPresetSpec | None | Unset = UNSET
    """Canary rollout policy for this source-ref deployment. Mutually exclusive with traffic_percent."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.canary_preset_spec import CanaryPresetSpec

        repo = self.repo

        ref = self.ref

        format_: str | Unset = UNSET
        if not isinstance(self.format_, Unset):
            format_ = self.format_

        reason = self.reason

        tag: str | Unset = UNSET
        if not isinstance(self.tag, Unset):
            tag = self.tag

        deployed_by = self.deployed_by

        pr_number = self.pr_number

        traffic_percent: int | None | Unset
        if isinstance(self.traffic_percent, Unset):
            traffic_percent = UNSET
        else:
            traffic_percent = self.traffic_percent

        canary: dict[str, Any] | None | Unset
        if isinstance(self.canary, Unset):
            canary = UNSET
        elif isinstance(self.canary, CanaryPresetSpec):
            canary = self.canary.to_dict()
        else:
            canary = self.canary

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "repo": repo,
                "ref": ref,
            }
        )
        if format_ is not UNSET:
            field_dict["format"] = format_
        if reason is not UNSET:
            field_dict["reason"] = reason
        if tag is not UNSET:
            field_dict["tag"] = tag
        if deployed_by is not UNSET:
            field_dict["deployed_by"] = deployed_by
        if pr_number is not UNSET:
            field_dict["pr_number"] = pr_number
        if traffic_percent is not UNSET:
            field_dict["traffic_percent"] = traffic_percent
        if canary is not UNSET:
            field_dict["canary"] = canary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.canary_preset_spec import CanaryPresetSpec

        d = dict(src_dict)
        repo = d.pop("repo")

        ref = d.pop("ref")

        _format_ = d.pop("format", UNSET)
        format_: SourceRefDeployRequestFormat | Unset
        if isinstance(_format_, Unset):
            format_ = UNSET
        else:
            format_ = check_source_ref_deploy_request_format(_format_)

        reason = d.pop("reason", UNSET)

        _tag = d.pop("tag", UNSET)
        tag: SourceRefDeployRequestTag | Unset
        if isinstance(_tag, Unset):
            tag = UNSET
        else:
            tag = check_source_ref_deploy_request_tag(_tag)

        deployed_by = d.pop("deployed_by", UNSET)

        pr_number = d.pop("pr_number", UNSET)

        def _parse_traffic_percent(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        traffic_percent = _parse_traffic_percent(d.pop("traffic_percent", UNSET))

        def _parse_canary(data: object) -> CanaryPresetSpec | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                canary_type_0 = CanaryPresetSpec.from_dict(data)

                return canary_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(CanaryPresetSpec | None | Unset, data)

        canary = _parse_canary(d.pop("canary", UNSET))

        source_ref_deploy_request = cls(
            repo=repo,
            ref=ref,
            format_=format_,
            reason=reason,
            tag=tag,
            deployed_by=deployed_by,
            pr_number=pr_number,
            traffic_percent=traffic_percent,
            canary=canary,
        )

        source_ref_deploy_request.additional_properties = d
        return source_ref_deploy_request

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

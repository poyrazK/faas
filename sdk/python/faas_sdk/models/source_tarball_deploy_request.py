from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.source_tarball_deploy_request_tag import (
    SourceTarballDeployRequestTag,
    check_source_tarball_deploy_request_tag,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.canary_preset_spec import CanaryPresetSpec


T = TypeVar("T", bound="SourceTarballDeployRequest")


@_attrs_define
class SourceTarballDeployRequest:
    """Body for the informational `sidecar` form field on POST /v1/apps/{slug}/deployments/source-tarball (issue #961 /
    Mega-A PR-1, ADR-115). The CLI is the trust root for this deploy path; apid does NOT consult `github_installations`
    and does NOT attempt a server-side git fetch. The sidecar fields are recorded on the build row for provenance only —
    the build pipeline does NOT use them to fetch upstream.

    """

    repo: None | str | Unset = UNSET
    """`owner/repo` from the customer's git remote, parsed by `cmd/gregale/git_local.go::parseGitRemoteURL`. nil
    when the sidecar is omitted entirely."""
    ref: None | str | Unset = UNSET
    """40-char lowercase SHA from `git rev-parse HEAD`. Informational only; the build pipeline does NOT pin to this
    SHA."""
    reason: str | Unset = UNSET
    """Free-form operator note on the tarball deploy request (≤280 chars). Example: 'Emergency rollback after
    payment provider incident'."""
    tag: SourceTarballDeployRequestTag | Unset = UNSET
    """Closed-set annotation tag on the tarball deploy request for grouping/filtering."""
    deployed_by: str | Unset = UNSET
    """Human-readable actor label on the tarball deploy request. CLI auto-captures from `git config user.name`;
    githubd stamps pusher.name; the GitHub Action defaults to ${{ github.actor }}."""
    pr_number: int | Unset = UNSET
    """Pull-request number that drove this tarball deploy request (githubd pull_request.number; Action ${{
    github.event.pull_request.number }}). NULL for push-to-main with no inferred PR."""
    traffic_percent: int | None | Unset = UNSET
    """Explicit initial traffic weight for this source-tarball deployment. Omitted uses the normal stable rollout;
    zero stages a dark live revision."""
    canary: CanaryPresetSpec | None | Unset = UNSET
    """Canary rollout policy for this source-tarball deployment. Mutually exclusive with traffic_percent."""
    rollback_on_5xx: bool | None | Unset = UNSET
    """Source-tarball deployment opt-in for first-wake 5xx auto-rollback; Pro/Scale only, with omitted or null
    defaulting to false."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.canary_preset_spec import CanaryPresetSpec

        repo: None | str | Unset
        if isinstance(self.repo, Unset):
            repo = UNSET
        else:
            repo = self.repo

        ref: None | str | Unset
        if isinstance(self.ref, Unset):
            ref = UNSET
        else:
            ref = self.ref

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

        rollback_on_5xx: bool | None | Unset
        if isinstance(self.rollback_on_5xx, Unset):
            rollback_on_5xx = UNSET
        else:
            rollback_on_5xx = self.rollback_on_5xx

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if repo is not UNSET:
            field_dict["repo"] = repo
        if ref is not UNSET:
            field_dict["ref"] = ref
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
        if rollback_on_5xx is not UNSET:
            field_dict["rollback_on_5xx"] = rollback_on_5xx

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.canary_preset_spec import CanaryPresetSpec

        d = dict(src_dict)

        def _parse_repo(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        repo = _parse_repo(d.pop("repo", UNSET))

        def _parse_ref(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        ref = _parse_ref(d.pop("ref", UNSET))

        reason = d.pop("reason", UNSET)

        _tag = d.pop("tag", UNSET)
        tag: SourceTarballDeployRequestTag | Unset
        if isinstance(_tag, Unset):
            tag = UNSET
        else:
            tag = check_source_tarball_deploy_request_tag(_tag)

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

        def _parse_rollback_on_5xx(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        rollback_on_5xx = _parse_rollback_on_5xx(d.pop("rollback_on_5xx", UNSET))

        source_tarball_deploy_request = cls(
            repo=repo,
            ref=ref,
            reason=reason,
            tag=tag,
            deployed_by=deployed_by,
            pr_number=pr_number,
            traffic_percent=traffic_percent,
            canary=canary,
            rollback_on_5xx=rollback_on_5xx,
        )

        source_tarball_deploy_request.additional_properties = d
        return source_tarball_deploy_request

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

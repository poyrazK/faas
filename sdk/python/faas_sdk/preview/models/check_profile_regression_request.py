from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_regression_options import ProfileRegressionOptions


T = TypeVar("T", bound="CheckProfileRegressionRequest")


@_attrs_define
class CheckProfileRegressionRequest:
    """Revision-protected on-demand assessment request. Omitted options use CPU/s, 20 percent, 0.01 CPU/s, three profiles
    and 0.8 capture ratio. CPU/request mode requires both request-specific options.

    """

    expected_revision: int
    options: ProfileRegressionOptions | Unset = UNSET
    """Threshold policy for sampled CPU per wall-clock second or per weighted observed request. Both relative and
    selected-metric absolute increase thresholds must be met. CPU-per-request mode also requires retained request
    telemetry and its configured minimum request count in both deployment windows; absent or sparse counts are
    inconclusive. Request telemetry rows use minute-bucket timestamps, so counts near window boundaries can be
    approximate and may be incomplete. Capture requirements apply separately to each profile window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        options: dict[str, Any] | Unset = UNSET
        if not isinstance(self.options, Unset):
            options = self.options.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_revision": expected_revision,
            }
        )
        if options is not UNSET:
            field_dict["options"] = options

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_regression_options import ProfileRegressionOptions

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        _options = d.pop("options", UNSET)
        options: ProfileRegressionOptions | Unset
        if isinstance(_options, Unset):
            options = UNSET
        else:
            options = ProfileRegressionOptions.from_dict(_options)

        check_profile_regression_request = cls(
            expected_revision=expected_revision,
            options=options,
        )

        check_profile_regression_request.additional_properties = d
        return check_profile_regression_request

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

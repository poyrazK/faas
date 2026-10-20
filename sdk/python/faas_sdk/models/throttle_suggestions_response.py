from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.throttle_preview_row import ThrottlePreviewRow
    from ..models.throttle_suggestion_row import ThrottleSuggestionRow


T = TypeVar("T", bound="ThrottleSuggestionsResponse")


@_attrs_define
class ThrottleSuggestionsResponse:
    """Per-route throttle recommendation payload (ADR-091 D20.5
    amendment, issue #881). Source is `prometheus` on success
    or `degraded: <reason>` on Prometheus failure (response is
    still 200 with empty Suggestions — the dashboard's
    empty-state branch handles it).

    Phase 4 D1/D2 (ADR-104 amendment 5): when the request
    supplies `dry_run=true` + `candidate_rps` + (optional)
    `candidate_burst`, the response ALSO carries a per-route
    `would_have_rejected` array + the static
    `per_consumer_limit_note` literal that names the
    `gateway_requests_by_route_total` label gap. Dry-run is a
    guard-rail for the customer's own probe value — not
    auto-apply.

    """

    app_id: str
    range_: str
    source: str
    """`prometheus` on success, `degraded: <reason>` on
    PromQL failure.
    """
    routes_collapsed: int
    """Count of routes that collapsed into the reserved
    `__route_other__` overflow bucket during the window
    (ADR-093 cap = 50). A non-zero value indicates the
    throttle is partial-coverage regardless of the
    configured limit.
    """
    plan_ceiling_rps: int
    """`plan.RateLimitRPS` — the sub-plan ceiling the
    suggestion is clamped to. 0 on unknown plans (fail
    OPEN — the apid sub-plan validator is the
    authoritative gate).
    """
    plan_ceiling_burst: int
    """`plan.RateLimitBurst` — the sub-plan ceiling for
    burst. 0 on unknown plans.
    """
    multiplier: float
    """The headroom factor the recommender applied to every
    route (`Multiplier` constant). Echoed on the wire
    so the strategy is auditable.
    """
    suggestions: list[ThrottleSuggestionRow]
    route_metrics_disabled: bool = False
    """True when `apps.route_metrics_enabled=false` (Free
    plan). The response carries empty Suggestions plus
    this flag so the dashboard can render the upsell
    rather than a misleading zero.
    """
    as_of: datetime.datetime | Unset = UNSET
    """RFC 3339 UTC timestamp the response was assembled."""
    dry_run: bool | Unset = UNSET
    """True iff the request supplied `dry_run=true`. The
    `would_have_rejected` + `per_consumer_limit_note`
    fields are only populated when this is true.
    """
    candidate_rps: float | Unset = UNSET
    """Echo of the customer's probe value (request
    `candidate_rps`). Surfaced so a customer reading
    the wire doesn't have to correlate the preview
    rows with the request they sent.
    """
    candidate_burst: int | Unset = UNSET
    """Echo of the customer's probe burst (request
    `candidate_burst`, optional).
    """
    would_have_rejected: list[ThrottlePreviewRow] | Unset = UNSET
    """One row per surviving route with the count of
    sub-windows where observed rps exceeded
    `candidate_rps` over the recommendation window.
    The preview counts at rule scope — see
    `per_consumer_limit_note` for the label-gap
    caveat.
    """
    per_consumer_limit_note: str | Unset = UNSET
    """Static literal naming the
    `gateway_requests_by_route_total` label gap (no
    per-consumer labels today). Surfaced so dashboards
    / CLIs reading the preview don't silently
    mis-attribute a rule-scope count to a
    per-consumer scope.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        range_ = self.range_

        source = self.source

        route_metrics_disabled = self.route_metrics_disabled

        routes_collapsed = self.routes_collapsed

        plan_ceiling_rps = self.plan_ceiling_rps

        plan_ceiling_burst = self.plan_ceiling_burst

        multiplier = self.multiplier

        suggestions = []
        for suggestions_item_data in self.suggestions:
            suggestions_item = suggestions_item_data.to_dict()
            suggestions.append(suggestions_item)

        as_of: str | Unset = UNSET
        if not isinstance(self.as_of, Unset):
            as_of = self.as_of.isoformat()

        dry_run = self.dry_run

        candidate_rps = self.candidate_rps

        candidate_burst = self.candidate_burst

        would_have_rejected: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.would_have_rejected, Unset):
            would_have_rejected = []
            for would_have_rejected_item_data in self.would_have_rejected:
                would_have_rejected_item = would_have_rejected_item_data.to_dict()
                would_have_rejected.append(would_have_rejected_item)

        per_consumer_limit_note = self.per_consumer_limit_note

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "range": range_,
                "source": source,
                "route_metrics_disabled": route_metrics_disabled,
                "routes_collapsed": routes_collapsed,
                "plan_ceiling_rps": plan_ceiling_rps,
                "plan_ceiling_burst": plan_ceiling_burst,
                "multiplier": multiplier,
                "suggestions": suggestions,
            }
        )
        if as_of is not UNSET:
            field_dict["as_of"] = as_of
        if dry_run is not UNSET:
            field_dict["dry_run"] = dry_run
        if candidate_rps is not UNSET:
            field_dict["candidate_rps"] = candidate_rps
        if candidate_burst is not UNSET:
            field_dict["candidate_burst"] = candidate_burst
        if would_have_rejected is not UNSET:
            field_dict["would_have_rejected"] = would_have_rejected
        if per_consumer_limit_note is not UNSET:
            field_dict["per_consumer_limit_note"] = per_consumer_limit_note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.throttle_preview_row import ThrottlePreviewRow
        from ..models.throttle_suggestion_row import ThrottleSuggestionRow

        d = dict(src_dict)
        app_id = d.pop("app_id")

        range_ = d.pop("range")

        source = d.pop("source")

        route_metrics_disabled = d.pop("route_metrics_disabled")

        routes_collapsed = d.pop("routes_collapsed")

        plan_ceiling_rps = d.pop("plan_ceiling_rps")

        plan_ceiling_burst = d.pop("plan_ceiling_burst")

        multiplier = d.pop("multiplier")

        suggestions = []
        _suggestions = d.pop("suggestions")
        for suggestions_item_data in _suggestions:
            suggestions_item = ThrottleSuggestionRow.from_dict(suggestions_item_data)

            suggestions.append(suggestions_item)

        _as_of = d.pop("as_of", UNSET)
        as_of: datetime.datetime | Unset
        if isinstance(_as_of, Unset):
            as_of = UNSET
        else:
            as_of = datetime.datetime.fromisoformat(_as_of)

        dry_run = d.pop("dry_run", UNSET)

        candidate_rps = d.pop("candidate_rps", UNSET)

        candidate_burst = d.pop("candidate_burst", UNSET)

        _would_have_rejected = d.pop("would_have_rejected", UNSET)
        would_have_rejected: list[ThrottlePreviewRow] | Unset = UNSET
        if _would_have_rejected is not UNSET:
            would_have_rejected = []
            for would_have_rejected_item_data in _would_have_rejected:
                would_have_rejected_item = ThrottlePreviewRow.from_dict(would_have_rejected_item_data)

                would_have_rejected.append(would_have_rejected_item)

        per_consumer_limit_note = d.pop("per_consumer_limit_note", UNSET)

        throttle_suggestions_response = cls(
            app_id=app_id,
            range_=range_,
            source=source,
            route_metrics_disabled=route_metrics_disabled,
            routes_collapsed=routes_collapsed,
            plan_ceiling_rps=plan_ceiling_rps,
            plan_ceiling_burst=plan_ceiling_burst,
            multiplier=multiplier,
            suggestions=suggestions,
            as_of=as_of,
            dry_run=dry_run,
            candidate_rps=candidate_rps,
            candidate_burst=candidate_burst,
            would_have_rejected=would_have_rejected,
            per_consumer_limit_note=per_consumer_limit_note,
        )

        throttle_suggestions_response.additional_properties = d
        return throttle_suggestions_response

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

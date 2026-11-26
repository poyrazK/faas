import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.alert_anomaly_preview_response import AlertAnomalyPreviewResponse
from ...models.preview_alert_anomaly_candidate_baseline import (
    PreviewAlertAnomalyCandidateBaseline,
)
from ...models.preview_alert_anomaly_comparison import (
    PreviewAlertAnomalyComparison,
)
from ...models.preview_alert_anomaly_metric import PreviewAlertAnomalyMetric
from ...models.preview_alert_anomaly_recovery_confirmation_checks import (
    PreviewAlertAnomalyRecoveryConfirmationChecks,
)
from ...models.preview_alert_anomaly_window_spec import (
    PreviewAlertAnomalyWindowSpec,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    metric: PreviewAlertAnomalyMetric | Unset = "request_count",
    comparison: PreviewAlertAnomalyComparison,
    threshold: float,
    candidate_threshold: float | Unset = UNSET,
    candidate_baseline: PreviewAlertAnomalyCandidateBaseline | Unset = UNSET,
    incident_replay_start: datetime.datetime | Unset = UNSET,
    environment_scope: str | Unset = UNSET,
    cooldown_minutes: int | Unset = UNSET,
    recovery_confirmation_checks: PreviewAlertAnomalyRecoveryConfirmationChecks | Unset = UNSET,
    window_spec: PreviewAlertAnomalyWindowSpec,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_metric: str | Unset = UNSET
    if not isinstance(metric, Unset):
        json_metric = metric

    params["metric"] = json_metric

    json_comparison: str = comparison
    params["comparison"] = json_comparison

    params["threshold"] = threshold

    params["candidate_threshold"] = candidate_threshold

    json_candidate_baseline: str | Unset = UNSET
    if not isinstance(candidate_baseline, Unset):
        json_candidate_baseline = candidate_baseline

    params["candidate_baseline"] = json_candidate_baseline

    json_incident_replay_start: str | Unset = UNSET
    if not isinstance(incident_replay_start, Unset):
        json_incident_replay_start = incident_replay_start.isoformat()
    params["incident_replay_start"] = json_incident_replay_start

    params["environment_scope"] = environment_scope

    params["cooldown_minutes"] = cooldown_minutes

    json_recovery_confirmation_checks: int | Unset = UNSET
    if not isinstance(recovery_confirmation_checks, Unset):
        json_recovery_confirmation_checks = recovery_confirmation_checks

    params["recovery_confirmation_checks"] = json_recovery_confirmation_checks

    json_window_spec: str = window_spec
    params["window_spec"] = json_window_spec

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/alerts/anomaly-preview".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AlertAnomalyPreviewResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AlertAnomalyPreviewResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AlertAnomalyPreviewResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    metric: PreviewAlertAnomalyMetric | Unset = "request_count",
    comparison: PreviewAlertAnomalyComparison,
    threshold: float,
    candidate_threshold: float | Unset = UNSET,
    candidate_baseline: PreviewAlertAnomalyCandidateBaseline | Unset = UNSET,
    incident_replay_start: datetime.datetime | Unset = UNSET,
    environment_scope: str | Unset = UNSET,
    cooldown_minutes: int | Unset = UNSET,
    recovery_confirmation_checks: PreviewAlertAnomalyRecoveryConfirmationChecks | Unset = UNSET,
    window_spec: PreviewAlertAnomalyWindowSpec,
) -> Response[AlertAnomalyPreviewResponse | Problem]:
    """Test a request-count, error-rate, or p95 latency anomaly rule on recent traffic.

     Previews seven days of hourly checkpoints by default. An incident replay previews a selected two-
    hour period at one-minute resolution and groups breaches into incidents with notification
    eligibility. Either mode can compare a different multiplier. Request-count rules can compare a same-
    weekday baseline. Error-rate rules can compare a zero-baseline fallback. Error-rate and p95 latency
    rules use the live traffic and minimum-value guards. P95 uses successful requests only. An incident
    replay can also compare two-check recovery confirmation. The preview is read-only.

    Args:
        slug (str):
        metric (PreviewAlertAnomalyMetric | Unset):  Default: 'request_count'.
        comparison (PreviewAlertAnomalyComparison):
        threshold (float):
        candidate_threshold (float | Unset):
        candidate_baseline (PreviewAlertAnomalyCandidateBaseline | Unset):
        incident_replay_start (datetime.datetime | Unset):
        environment_scope (str | Unset):
        cooldown_minutes (int | Unset):
        recovery_confirmation_checks (PreviewAlertAnomalyRecoveryConfirmationChecks | Unset):
        window_spec (PreviewAlertAnomalyWindowSpec):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AlertAnomalyPreviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        metric=metric,
        comparison=comparison,
        threshold=threshold,
        candidate_threshold=candidate_threshold,
        candidate_baseline=candidate_baseline,
        incident_replay_start=incident_replay_start,
        environment_scope=environment_scope,
        cooldown_minutes=cooldown_minutes,
        recovery_confirmation_checks=recovery_confirmation_checks,
        window_spec=window_spec,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    metric: PreviewAlertAnomalyMetric | Unset = "request_count",
    comparison: PreviewAlertAnomalyComparison,
    threshold: float,
    candidate_threshold: float | Unset = UNSET,
    candidate_baseline: PreviewAlertAnomalyCandidateBaseline | Unset = UNSET,
    incident_replay_start: datetime.datetime | Unset = UNSET,
    environment_scope: str | Unset = UNSET,
    cooldown_minutes: int | Unset = UNSET,
    recovery_confirmation_checks: PreviewAlertAnomalyRecoveryConfirmationChecks | Unset = UNSET,
    window_spec: PreviewAlertAnomalyWindowSpec,
) -> AlertAnomalyPreviewResponse | Problem | None:
    """Test a request-count, error-rate, or p95 latency anomaly rule on recent traffic.

     Previews seven days of hourly checkpoints by default. An incident replay previews a selected two-
    hour period at one-minute resolution and groups breaches into incidents with notification
    eligibility. Either mode can compare a different multiplier. Request-count rules can compare a same-
    weekday baseline. Error-rate rules can compare a zero-baseline fallback. Error-rate and p95 latency
    rules use the live traffic and minimum-value guards. P95 uses successful requests only. An incident
    replay can also compare two-check recovery confirmation. The preview is read-only.

    Args:
        slug (str):
        metric (PreviewAlertAnomalyMetric | Unset):  Default: 'request_count'.
        comparison (PreviewAlertAnomalyComparison):
        threshold (float):
        candidate_threshold (float | Unset):
        candidate_baseline (PreviewAlertAnomalyCandidateBaseline | Unset):
        incident_replay_start (datetime.datetime | Unset):
        environment_scope (str | Unset):
        cooldown_minutes (int | Unset):
        recovery_confirmation_checks (PreviewAlertAnomalyRecoveryConfirmationChecks | Unset):
        window_spec (PreviewAlertAnomalyWindowSpec):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AlertAnomalyPreviewResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        metric=metric,
        comparison=comparison,
        threshold=threshold,
        candidate_threshold=candidate_threshold,
        candidate_baseline=candidate_baseline,
        incident_replay_start=incident_replay_start,
        environment_scope=environment_scope,
        cooldown_minutes=cooldown_minutes,
        recovery_confirmation_checks=recovery_confirmation_checks,
        window_spec=window_spec,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    metric: PreviewAlertAnomalyMetric | Unset = "request_count",
    comparison: PreviewAlertAnomalyComparison,
    threshold: float,
    candidate_threshold: float | Unset = UNSET,
    candidate_baseline: PreviewAlertAnomalyCandidateBaseline | Unset = UNSET,
    incident_replay_start: datetime.datetime | Unset = UNSET,
    environment_scope: str | Unset = UNSET,
    cooldown_minutes: int | Unset = UNSET,
    recovery_confirmation_checks: PreviewAlertAnomalyRecoveryConfirmationChecks | Unset = UNSET,
    window_spec: PreviewAlertAnomalyWindowSpec,
) -> Response[AlertAnomalyPreviewResponse | Problem]:
    """Test a request-count, error-rate, or p95 latency anomaly rule on recent traffic.

     Previews seven days of hourly checkpoints by default. An incident replay previews a selected two-
    hour period at one-minute resolution and groups breaches into incidents with notification
    eligibility. Either mode can compare a different multiplier. Request-count rules can compare a same-
    weekday baseline. Error-rate rules can compare a zero-baseline fallback. Error-rate and p95 latency
    rules use the live traffic and minimum-value guards. P95 uses successful requests only. An incident
    replay can also compare two-check recovery confirmation. The preview is read-only.

    Args:
        slug (str):
        metric (PreviewAlertAnomalyMetric | Unset):  Default: 'request_count'.
        comparison (PreviewAlertAnomalyComparison):
        threshold (float):
        candidate_threshold (float | Unset):
        candidate_baseline (PreviewAlertAnomalyCandidateBaseline | Unset):
        incident_replay_start (datetime.datetime | Unset):
        environment_scope (str | Unset):
        cooldown_minutes (int | Unset):
        recovery_confirmation_checks (PreviewAlertAnomalyRecoveryConfirmationChecks | Unset):
        window_spec (PreviewAlertAnomalyWindowSpec):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AlertAnomalyPreviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        metric=metric,
        comparison=comparison,
        threshold=threshold,
        candidate_threshold=candidate_threshold,
        candidate_baseline=candidate_baseline,
        incident_replay_start=incident_replay_start,
        environment_scope=environment_scope,
        cooldown_minutes=cooldown_minutes,
        recovery_confirmation_checks=recovery_confirmation_checks,
        window_spec=window_spec,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    metric: PreviewAlertAnomalyMetric | Unset = "request_count",
    comparison: PreviewAlertAnomalyComparison,
    threshold: float,
    candidate_threshold: float | Unset = UNSET,
    candidate_baseline: PreviewAlertAnomalyCandidateBaseline | Unset = UNSET,
    incident_replay_start: datetime.datetime | Unset = UNSET,
    environment_scope: str | Unset = UNSET,
    cooldown_minutes: int | Unset = UNSET,
    recovery_confirmation_checks: PreviewAlertAnomalyRecoveryConfirmationChecks | Unset = UNSET,
    window_spec: PreviewAlertAnomalyWindowSpec,
) -> AlertAnomalyPreviewResponse | Problem | None:
    """Test a request-count, error-rate, or p95 latency anomaly rule on recent traffic.

     Previews seven days of hourly checkpoints by default. An incident replay previews a selected two-
    hour period at one-minute resolution and groups breaches into incidents with notification
    eligibility. Either mode can compare a different multiplier. Request-count rules can compare a same-
    weekday baseline. Error-rate rules can compare a zero-baseline fallback. Error-rate and p95 latency
    rules use the live traffic and minimum-value guards. P95 uses successful requests only. An incident
    replay can also compare two-check recovery confirmation. The preview is read-only.

    Args:
        slug (str):
        metric (PreviewAlertAnomalyMetric | Unset):  Default: 'request_count'.
        comparison (PreviewAlertAnomalyComparison):
        threshold (float):
        candidate_threshold (float | Unset):
        candidate_baseline (PreviewAlertAnomalyCandidateBaseline | Unset):
        incident_replay_start (datetime.datetime | Unset):
        environment_scope (str | Unset):
        cooldown_minutes (int | Unset):
        recovery_confirmation_checks (PreviewAlertAnomalyRecoveryConfirmationChecks | Unset):
        window_spec (PreviewAlertAnomalyWindowSpec):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AlertAnomalyPreviewResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            candidate_threshold=candidate_threshold,
            candidate_baseline=candidate_baseline,
            incident_replay_start=incident_replay_start,
            environment_scope=environment_scope,
            cooldown_minutes=cooldown_minutes,
            recovery_confirmation_checks=recovery_confirmation_checks,
            window_spec=window_spec,
        )
    ).parsed

from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_health_response import ObsHealthResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/health",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObsHealthResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ObsHealthResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObsHealthResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObsHealthResponse | Problem]:
    """Meta-obs health snapshot — audit write rates, outcome-missing counts, trace_id completeness, alert
    firing count (admin-only).

     Operator-side meta-observation endpoint. Composes a single
    JSON snapshot from:

      - `apid_audit_log_write_total[5m]` / `apid_audit_log_write_failures_total[5m]`
        — the API's own Prometheus counters.
      - `audit_log_coverage_ratio_5m` — SQL trace coverage over the durable
        audit_log table, including every audit kind.
      - `SELECT kind, count(*) FROM operator_intents WHERE
        status = 'running' AND started_at < now() - interval
        '5 minutes' GROUP BY kind` — single SQL query.
      - `SELECT kind, count(*) FILTER (WHERE trace_id IS NOT
        NULL)::float / count(*) FROM events WHERE kind LIKE
        'operator.action.%' AND at > now() - interval '5
        minutes' GROUP BY kind` — reads events (live), NOT
        audit_log (FK-free post-deletion copy).
      - A Prometheus count of firing alerts, excluding FaasWatchdog
        and info-only alerts.

    Federation is out of scope (each daemon owns its own
    /metrics); this endpoint is the local API's view. Kinds
    with zero rows in the SQL-derived fields are seeded to
    0 (counts) or 1.0 (ratios, vacuous truth) so the JSON
    shape stays stable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObsHealthResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> ObsHealthResponse | Problem | None:
    """Meta-obs health snapshot — audit write rates, outcome-missing counts, trace_id completeness, alert
    firing count (admin-only).

     Operator-side meta-observation endpoint. Composes a single
    JSON snapshot from:

      - `apid_audit_log_write_total[5m]` / `apid_audit_log_write_failures_total[5m]`
        — the API's own Prometheus counters.
      - `audit_log_coverage_ratio_5m` — SQL trace coverage over the durable
        audit_log table, including every audit kind.
      - `SELECT kind, count(*) FROM operator_intents WHERE
        status = 'running' AND started_at < now() - interval
        '5 minutes' GROUP BY kind` — single SQL query.
      - `SELECT kind, count(*) FILTER (WHERE trace_id IS NOT
        NULL)::float / count(*) FROM events WHERE kind LIKE
        'operator.action.%' AND at > now() - interval '5
        minutes' GROUP BY kind` — reads events (live), NOT
        audit_log (FK-free post-deletion copy).
      - A Prometheus count of firing alerts, excluding FaasWatchdog
        and info-only alerts.

    Federation is out of scope (each daemon owns its own
    /metrics); this endpoint is the local API's view. Kinds
    with zero rows in the SQL-derived fields are seeded to
    0 (counts) or 1.0 (ratios, vacuous truth) so the JSON
    shape stays stable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObsHealthResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObsHealthResponse | Problem]:
    """Meta-obs health snapshot — audit write rates, outcome-missing counts, trace_id completeness, alert
    firing count (admin-only).

     Operator-side meta-observation endpoint. Composes a single
    JSON snapshot from:

      - `apid_audit_log_write_total[5m]` / `apid_audit_log_write_failures_total[5m]`
        — the API's own Prometheus counters.
      - `audit_log_coverage_ratio_5m` — SQL trace coverage over the durable
        audit_log table, including every audit kind.
      - `SELECT kind, count(*) FROM operator_intents WHERE
        status = 'running' AND started_at < now() - interval
        '5 minutes' GROUP BY kind` — single SQL query.
      - `SELECT kind, count(*) FILTER (WHERE trace_id IS NOT
        NULL)::float / count(*) FROM events WHERE kind LIKE
        'operator.action.%' AND at > now() - interval '5
        minutes' GROUP BY kind` — reads events (live), NOT
        audit_log (FK-free post-deletion copy).
      - A Prometheus count of firing alerts, excluding FaasWatchdog
        and info-only alerts.

    Federation is out of scope (each daemon owns its own
    /metrics); this endpoint is the local API's view. Kinds
    with zero rows in the SQL-derived fields are seeded to
    0 (counts) or 1.0 (ratios, vacuous truth) so the JSON
    shape stays stable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObsHealthResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> ObsHealthResponse | Problem | None:
    """Meta-obs health snapshot — audit write rates, outcome-missing counts, trace_id completeness, alert
    firing count (admin-only).

     Operator-side meta-observation endpoint. Composes a single
    JSON snapshot from:

      - `apid_audit_log_write_total[5m]` / `apid_audit_log_write_failures_total[5m]`
        — the API's own Prometheus counters.
      - `audit_log_coverage_ratio_5m` — SQL trace coverage over the durable
        audit_log table, including every audit kind.
      - `SELECT kind, count(*) FROM operator_intents WHERE
        status = 'running' AND started_at < now() - interval
        '5 minutes' GROUP BY kind` — single SQL query.
      - `SELECT kind, count(*) FILTER (WHERE trace_id IS NOT
        NULL)::float / count(*) FROM events WHERE kind LIKE
        'operator.action.%' AND at > now() - interval '5
        minutes' GROUP BY kind` — reads events (live), NOT
        audit_log (FK-free post-deletion copy).
      - A Prometheus count of firing alerts, excluding FaasWatchdog
        and info-only alerts.

    Federation is out of scope (each daemon owns its own
    /metrics); this endpoint is the local API's view. Kinds
    with zero rows in the SQL-derived fields are seeded to
    0 (counts) or 1.0 (ratios, vacuous truth) so the JSON
    shape stays stable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObsHealthResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed

from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.scan_result import ScanResult
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/scan".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | ScanResult | None:
    if response.status_code == 200:
        response_200 = ScanResult.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScanResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ScanResult]:
    r"""Get per-deploy grype scan.

     Returns the per-deploy grype CVE scan payload (issue #464 /
    ADR-055). The scan runs on the per-app layer ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before the
    pending→snapshotting transition) and lands on the
    `deployments` row.

    Status field is the closed enum:
      - `complete` — full payload (SeverityCounts + Vulnerabilities).
      - `failed` — payload carries `error` only; SeverityCounts is
        all-zero, Vulnerabilities is nil. Rendered as the
        \"scan failed\" chip on the dashboard.
      - `skipped` — pre-feature backfill row
        (migrations/00135 stamps `scan_status='skipped'` on every
        pre-#464 row). Payload carries the reason sentinel.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak),
      - no scan has run yet (the deploy is still mid-pipeline or
        the row predates #464 entirely).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScanResult]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ScanResult | None:
    r"""Get per-deploy grype scan.

     Returns the per-deploy grype CVE scan payload (issue #464 /
    ADR-055). The scan runs on the per-app layer ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before the
    pending→snapshotting transition) and lands on the
    `deployments` row.

    Status field is the closed enum:
      - `complete` — full payload (SeverityCounts + Vulnerabilities).
      - `failed` — payload carries `error` only; SeverityCounts is
        all-zero, Vulnerabilities is nil. Rendered as the
        \"scan failed\" chip on the dashboard.
      - `skipped` — pre-feature backfill row
        (migrations/00135 stamps `scan_status='skipped'` on every
        pre-#464 row). Payload carries the reason sentinel.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak),
      - no scan has run yet (the deploy is still mid-pipeline or
        the row predates #464 entirely).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScanResult
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ScanResult]:
    r"""Get per-deploy grype scan.

     Returns the per-deploy grype CVE scan payload (issue #464 /
    ADR-055). The scan runs on the per-app layer ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before the
    pending→snapshotting transition) and lands on the
    `deployments` row.

    Status field is the closed enum:
      - `complete` — full payload (SeverityCounts + Vulnerabilities).
      - `failed` — payload carries `error` only; SeverityCounts is
        all-zero, Vulnerabilities is nil. Rendered as the
        \"scan failed\" chip on the dashboard.
      - `skipped` — pre-feature backfill row
        (migrations/00135 stamps `scan_status='skipped'` on every
        pre-#464 row). Payload carries the reason sentinel.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak),
      - no scan has run yet (the deploy is still mid-pipeline or
        the row predates #464 entirely).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScanResult]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ScanResult | None:
    r"""Get per-deploy grype scan.

     Returns the per-deploy grype CVE scan payload (issue #464 /
    ADR-055). The scan runs on the per-app layer ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before the
    pending→snapshotting transition) and lands on the
    `deployments` row.

    Status field is the closed enum:
      - `complete` — full payload (SeverityCounts + Vulnerabilities).
      - `failed` — payload carries `error` only; SeverityCounts is
        all-zero, Vulnerabilities is nil. Rendered as the
        \"scan failed\" chip on the dashboard.
      - `skipped` — pre-feature backfill row
        (migrations/00135 stamps `scan_status='skipped'` on every
        pre-#464 row). Payload carries the reason sentinel.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak),
      - no scan has run yet (the deploy is still mid-pipeline or
        the row predates #464 entirely).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScanResult
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed

from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.secret_scan_result import SecretScanResult
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/secret-scan".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SecretScanResult | None:
    if response.status_code == 200:
        response_200 = SecretScanResult.from_dict(response.json())

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
) -> Response[Problem | SecretScanResult]:
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
) -> Response[Problem | SecretScanResult]:
    """Get per-deploy image-layer secret scan.

     Returns the per-deploy secret-scan audit row (PR-A /
    ADR-101). The scan runs on the per-app ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before
    the pending→snapshotting transition) using the same
    `pkg/secretscan` engine the apid source-tree rejection
    path uses — same patterns, same providers, same Severity
    table, same snippet policy.

    Status field is the closed enum:
      - `complete` — image layer walked clean; `findings=[]`.
        Stamped on every scan (clean OR hit) so the dashboard
        renders the audit row immediately after the build.
      - `complete_with_redactions` — at least one finding
        landed in the audit row; `error_code =
        'image_secret_detected'` on the deployment. The
        deploy's pending→snapshotting transition does NOT
        fire.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak),
      - no scan has run yet (the deploy is still
        mid-pipeline or the row predates PR-A entirely).

    Each finding carries a `layer` label that attributes the
    finding to the per-walk source (`app` for the main image,
    `sidecar-<slug>` for each sidecar). Pre-PR-A rows
    (rejected source-tree bytes via the v2 422 path) carry
    `layer` empty or absent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SecretScanResult]
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
) -> Problem | SecretScanResult | None:
    """Get per-deploy image-layer secret scan.

     Returns the per-deploy secret-scan audit row (PR-A /
    ADR-101). The scan runs on the per-app ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before
    the pending→snapshotting transition) using the same
    `pkg/secretscan` engine the apid source-tree rejection
    path uses — same patterns, same providers, same Severity
    table, same snippet policy.

    Status field is the closed enum:
      - `complete` — image layer walked clean; `findings=[]`.
        Stamped on every scan (clean OR hit) so the dashboard
        renders the audit row immediately after the build.
      - `complete_with_redactions` — at least one finding
        landed in the audit row; `error_code =
        'image_secret_detected'` on the deployment. The
        deploy's pending→snapshotting transition does NOT
        fire.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak),
      - no scan has run yet (the deploy is still
        mid-pipeline or the row predates PR-A entirely).

    Each finding carries a `layer` label that attributes the
    finding to the per-walk source (`app` for the main image,
    `sidecar-<slug>` for each sidecar). Pre-PR-A rows
    (rejected source-tree bytes via the v2 422 path) carry
    `layer` empty or absent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SecretScanResult
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | SecretScanResult]:
    """Get per-deploy image-layer secret scan.

     Returns the per-deploy secret-scan audit row (PR-A /
    ADR-101). The scan runs on the per-app ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before
    the pending→snapshotting transition) using the same
    `pkg/secretscan` engine the apid source-tree rejection
    path uses — same patterns, same providers, same Severity
    table, same snippet policy.

    Status field is the closed enum:
      - `complete` — image layer walked clean; `findings=[]`.
        Stamped on every scan (clean OR hit) so the dashboard
        renders the audit row immediately after the build.
      - `complete_with_redactions` — at least one finding
        landed in the audit row; `error_code =
        'image_secret_detected'` on the deployment. The
        deploy's pending→snapshotting transition does NOT
        fire.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak),
      - no scan has run yet (the deploy is still
        mid-pipeline or the row predates PR-A entirely).

    Each finding carries a `layer` label that attributes the
    finding to the per-walk source (`app` for the main image,
    `sidecar-<slug>` for each sidecar). Pre-PR-A rows
    (rejected source-tree bytes via the v2 422 path) carry
    `layer` empty or absent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SecretScanResult]
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
) -> Problem | SecretScanResult | None:
    """Get per-deploy image-layer secret scan.

     Returns the per-deploy secret-scan audit row (PR-A /
    ADR-101). The scan runs on the per-app ext4 in imaged's
    deploy-complete path (after `SetDeploymentRootfs`, before
    the pending→snapshotting transition) using the same
    `pkg/secretscan` engine the apid source-tree rejection
    path uses — same patterns, same providers, same Severity
    table, same snippet policy.

    Status field is the closed enum:
      - `complete` — image layer walked clean; `findings=[]`.
        Stamped on every scan (clean OR hit) so the dashboard
        renders the audit row immediately after the build.
      - `complete_with_redactions` — at least one finding
        landed in the audit row; `error_code =
        'image_secret_detected'` on the deployment. The
        deploy's pending→snapshotting transition does NOT
        fire.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak),
      - no scan has run yet (the deploy is still
        mid-pipeline or the row predates PR-A entirely).

    Each finding carries a `layer` label that attributes the
    finding to the per-walk source (`app` for the main image,
    `sidecar-<slug>` for each sidecar). Pre-PR-A rows
    (rejected source-tree bytes via the v2 422 path) carry
    `layer` empty or absent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SecretScanResult
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed

from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_preview_url import DeploymentPreviewURL
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/url".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentPreviewURL | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentPreviewURL.from_dict(response.json())

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
) -> Response[DeploymentPreviewURL | Problem]:
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
) -> Response[DeploymentPreviewURL | Problem]:
    r"""Get per-deployment preview URL (SAFE-RELEASES-C.2).

     Returns the per-deployment preview URL shape
    `deploy-{N}.{slug}.gregale.dev` that the cert allowlist will
    mint under for a single deployment (issue #976 / ADR-122).
    `N` is the per-app 1-based ordinal of the deployment row,
    resolved from state.DeploymentOrdinal — the order is
    stable so a previously-issued URL doesn't silently rot when
    a later deploy lands.

    The `alive` field is the same predicate the cert allowlist
    consults (state.Deployment.DeploymentPreviewActive):
    `true` iff the deployment's status is in
    `{pending, building, imaging, snapshotting, live}`. When
    `alive=false` the handler returns 200 with `host=\"\"` and
    `url=\"\"` so the dashboard renders a \"preview closed\" chip
    without round-tripping again. When the per-deployment
    preview zone is disabled (`wire.DeployWildcardSuffix == \"\"`)
    the handler returns the same 200 + Alive=false shape so
    envelopes stay stable across environments.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentPreviewURL | Problem]
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
) -> DeploymentPreviewURL | Problem | None:
    r"""Get per-deployment preview URL (SAFE-RELEASES-C.2).

     Returns the per-deployment preview URL shape
    `deploy-{N}.{slug}.gregale.dev` that the cert allowlist will
    mint under for a single deployment (issue #976 / ADR-122).
    `N` is the per-app 1-based ordinal of the deployment row,
    resolved from state.DeploymentOrdinal — the order is
    stable so a previously-issued URL doesn't silently rot when
    a later deploy lands.

    The `alive` field is the same predicate the cert allowlist
    consults (state.Deployment.DeploymentPreviewActive):
    `true` iff the deployment's status is in
    `{pending, building, imaging, snapshotting, live}`. When
    `alive=false` the handler returns 200 with `host=\"\"` and
    `url=\"\"` so the dashboard renders a \"preview closed\" chip
    without round-tripping again. When the per-deployment
    preview zone is disabled (`wire.DeployWildcardSuffix == \"\"`)
    the handler returns the same 200 + Alive=false shape so
    envelopes stay stable across environments.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentPreviewURL | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentPreviewURL | Problem]:
    r"""Get per-deployment preview URL (SAFE-RELEASES-C.2).

     Returns the per-deployment preview URL shape
    `deploy-{N}.{slug}.gregale.dev` that the cert allowlist will
    mint under for a single deployment (issue #976 / ADR-122).
    `N` is the per-app 1-based ordinal of the deployment row,
    resolved from state.DeploymentOrdinal — the order is
    stable so a previously-issued URL doesn't silently rot when
    a later deploy lands.

    The `alive` field is the same predicate the cert allowlist
    consults (state.Deployment.DeploymentPreviewActive):
    `true` iff the deployment's status is in
    `{pending, building, imaging, snapshotting, live}`. When
    `alive=false` the handler returns 200 with `host=\"\"` and
    `url=\"\"` so the dashboard renders a \"preview closed\" chip
    without round-tripping again. When the per-deployment
    preview zone is disabled (`wire.DeployWildcardSuffix == \"\"`)
    the handler returns the same 200 + Alive=false shape so
    envelopes stay stable across environments.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentPreviewURL | Problem]
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
) -> DeploymentPreviewURL | Problem | None:
    r"""Get per-deployment preview URL (SAFE-RELEASES-C.2).

     Returns the per-deployment preview URL shape
    `deploy-{N}.{slug}.gregale.dev` that the cert allowlist will
    mint under for a single deployment (issue #976 / ADR-122).
    `N` is the per-app 1-based ordinal of the deployment row,
    resolved from state.DeploymentOrdinal — the order is
    stable so a previously-issued URL doesn't silently rot when
    a later deploy lands.

    The `alive` field is the same predicate the cert allowlist
    consults (state.Deployment.DeploymentPreviewActive):
    `true` iff the deployment's status is in
    `{pending, building, imaging, snapshotting, live}`. When
    `alive=false` the handler returns 200 with `host=\"\"` and
    `url=\"\"` so the dashboard renders a \"preview closed\" chip
    without round-tripping again. When the per-deployment
    preview zone is disabled (`wire.DeployWildcardSuffix == \"\"`)
    the handler returns the same 200 + Alive=false shape so
    envelopes stay stable across environments.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account
        (IDOR-safe; no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentPreviewURL | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed

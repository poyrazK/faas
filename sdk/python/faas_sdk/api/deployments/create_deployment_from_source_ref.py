from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...models.source_ref_deploy_request import SourceRefDeployRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: SourceRefDeployRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/source-ref".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponse | Problem | None:
    if response.status_code == 202:
        response_202 = DeploymentResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[DeploymentResponse | Problem]:
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
    body: SourceRefDeployRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create a deployment from a Git source-ref (headless).

     Headless deploy path (issue #739 / DEPLOY-PROV-4 / ADR-092).
    Resolves the GitHub install bound to the caller's account,
    fetches the (repo, ref) tarball via the githubd bridge, spools
    it under the per-plan SourceTarballMaxMB cap, validates shape,
    and enqueues a build (Kind=DeploymentKindGitHub) pinned to
    the resolved 40-char commit SHA.

    Designed for CI runners: bearer token only, no GitHub env
    vars required. Idempotency-Key collapses concurrent / retried
    CI jobs into one build row.

    Distinct from the dashboard bind path (`POST
    /v1/apps/{slug}/deployments` with a `source` multipart
    upload) which goes through the browser + UI bind picker.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (SourceRefDeployRequest): JSON body for POST /v1/apps/{slug}/deployments/source-ref
            (DEPLOY-PROV-4 / ADR-092, issue #739). The headless CI deploy
            path: `repo` resolves to an install-token-bound fetch, `ref` is
            the customer's chosen input (branch / tag / SHA — server
            resolves to a 40-char SHA before stamping the deployment row).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SourceRefDeployRequest,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create a deployment from a Git source-ref (headless).

     Headless deploy path (issue #739 / DEPLOY-PROV-4 / ADR-092).
    Resolves the GitHub install bound to the caller's account,
    fetches the (repo, ref) tarball via the githubd bridge, spools
    it under the per-plan SourceTarballMaxMB cap, validates shape,
    and enqueues a build (Kind=DeploymentKindGitHub) pinned to
    the resolved 40-char commit SHA.

    Designed for CI runners: bearer token only, no GitHub env
    vars required. Idempotency-Key collapses concurrent / retried
    CI jobs into one build row.

    Distinct from the dashboard bind path (`POST
    /v1/apps/{slug}/deployments` with a `source` multipart
    upload) which goes through the browser + UI bind picker.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (SourceRefDeployRequest): JSON body for POST /v1/apps/{slug}/deployments/source-ref
            (DEPLOY-PROV-4 / ADR-092, issue #739). The headless CI deploy
            path: `repo` resolves to an install-token-bound fetch, `ref` is
            the customer's chosen input (branch / tag / SHA — server
            resolves to a 40-char SHA before stamping the deployment row).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SourceRefDeployRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create a deployment from a Git source-ref (headless).

     Headless deploy path (issue #739 / DEPLOY-PROV-4 / ADR-092).
    Resolves the GitHub install bound to the caller's account,
    fetches the (repo, ref) tarball via the githubd bridge, spools
    it under the per-plan SourceTarballMaxMB cap, validates shape,
    and enqueues a build (Kind=DeploymentKindGitHub) pinned to
    the resolved 40-char commit SHA.

    Designed for CI runners: bearer token only, no GitHub env
    vars required. Idempotency-Key collapses concurrent / retried
    CI jobs into one build row.

    Distinct from the dashboard bind path (`POST
    /v1/apps/{slug}/deployments` with a `source` multipart
    upload) which goes through the browser + UI bind picker.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (SourceRefDeployRequest): JSON body for POST /v1/apps/{slug}/deployments/source-ref
            (DEPLOY-PROV-4 / ADR-092, issue #739). The headless CI deploy
            path: `repo` resolves to an install-token-bound fetch, `ref` is
            the customer's chosen input (branch / tag / SHA — server
            resolves to a 40-char SHA before stamping the deployment row).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SourceRefDeployRequest,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create a deployment from a Git source-ref (headless).

     Headless deploy path (issue #739 / DEPLOY-PROV-4 / ADR-092).
    Resolves the GitHub install bound to the caller's account,
    fetches the (repo, ref) tarball via the githubd bridge, spools
    it under the per-plan SourceTarballMaxMB cap, validates shape,
    and enqueues a build (Kind=DeploymentKindGitHub) pinned to
    the resolved 40-char commit SHA.

    Designed for CI runners: bearer token only, no GitHub env
    vars required. Idempotency-Key collapses concurrent / retried
    CI jobs into one build row.

    Distinct from the dashboard bind path (`POST
    /v1/apps/{slug}/deployments` with a `source` multipart
    upload) which goes through the browser + UI bind picker.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (SourceRefDeployRequest): JSON body for POST /v1/apps/{slug}/deployments/source-ref
            (DEPLOY-PROV-4 / ADR-092, issue #739). The headless CI deploy
            path: `repo` resolves to an install-token-bound fetch, `ref` is
            the customer's chosen input (branch / tag / SHA — server
            resolves to a 40-char SHA before stamping the deployment row).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed

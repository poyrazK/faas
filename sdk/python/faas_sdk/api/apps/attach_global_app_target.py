from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.attach_global_app_target_request import AttachGlobalAppTargetRequest
from ...models.global_app_target_response import GlobalAppTargetResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    region: str,
    *,
    body: AttachGlobalAppTargetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/global-apps/{slug}/targets/{region}".format(
            slug=quote(str(slug), safe=""),
            region=quote(str(region), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GlobalAppTargetResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GlobalAppTargetResponse.from_dict(response.json())

        return response_200

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GlobalAppTargetResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: AttachGlobalAppTargetRequest,
) -> Response[GlobalAppTargetResponse | Problem]:
    """Verify and attach an existing regional app.

     Requires personal directory-account deploy-write access and verified email. Probes only the
    registered HTTPS origin with the supplied regional personal account-admin key; never follows
    redirects or retains that key. Checks cell UUID, region and app ownership and atomically fences
    registry generation. Same-identity retries always repeat verification; responses are never replay-
    cached. Existing identities cannot be replaced without explicit detachment. Shared organization apps
    are unsupported.

    Args:
        slug (str):
        region (str):
        body (AttachGlobalAppTargetRequest): One-request proof of regional ownership. The key is
            never retained or returned. Body is bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalAppTargetResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        region=region,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: AttachGlobalAppTargetRequest,
) -> GlobalAppTargetResponse | Problem | None:
    """Verify and attach an existing regional app.

     Requires personal directory-account deploy-write access and verified email. Probes only the
    registered HTTPS origin with the supplied regional personal account-admin key; never follows
    redirects or retains that key. Checks cell UUID, region and app ownership and atomically fences
    registry generation. Same-identity retries always repeat verification; responses are never replay-
    cached. Existing identities cannot be replaced without explicit detachment. Shared organization apps
    are unsupported.

    Args:
        slug (str):
        region (str):
        body (AttachGlobalAppTargetRequest): One-request proof of regional ownership. The key is
            never retained or returned. Body is bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalAppTargetResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        region=region,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: AttachGlobalAppTargetRequest,
) -> Response[GlobalAppTargetResponse | Problem]:
    """Verify and attach an existing regional app.

     Requires personal directory-account deploy-write access and verified email. Probes only the
    registered HTTPS origin with the supplied regional personal account-admin key; never follows
    redirects or retains that key. Checks cell UUID, region and app ownership and atomically fences
    registry generation. Same-identity retries always repeat verification; responses are never replay-
    cached. Existing identities cannot be replaced without explicit detachment. Shared organization apps
    are unsupported.

    Args:
        slug (str):
        region (str):
        body (AttachGlobalAppTargetRequest): One-request proof of regional ownership. The key is
            never retained or returned. Body is bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalAppTargetResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        region=region,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: AttachGlobalAppTargetRequest,
) -> GlobalAppTargetResponse | Problem | None:
    """Verify and attach an existing regional app.

     Requires personal directory-account deploy-write access and verified email. Probes only the
    registered HTTPS origin with the supplied regional personal account-admin key; never follows
    redirects or retains that key. Checks cell UUID, region and app ownership and atomically fences
    registry generation. Same-identity retries always repeat verification; responses are never replay-
    cached. Existing identities cannot be replaced without explicit detachment. Shared organization apps
    are unsupported.

    Args:
        slug (str):
        region (str):
        body (AttachGlobalAppTargetRequest): One-request proof of regional ownership. The key is
            never retained or returned. Body is bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalAppTargetResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            region=region,
            client=client,
            body=body,
        )
    ).parsed

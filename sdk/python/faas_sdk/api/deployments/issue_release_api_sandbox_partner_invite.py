from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_partner_invite_response import APISandboxPartnerInviteResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandboxes/{sandbox}/partner/invite".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            sandbox=quote(str(sandbox), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxPartnerInviteResponse | Problem | None:
    if response.status_code == 201:
        response_201 = APISandboxPartnerInviteResponse.from_dict(response.json())

        return response_201

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxPartnerInviteResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APISandboxPartnerInviteResponse | Problem]:
    """Issue a one-time invite for a private partner copy.

     The owner can issue a link during provisioning. A new link invalidates any unredeemed link for the
    copy.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxPartnerInviteResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APISandboxPartnerInviteResponse | Problem | None:
    """Issue a one-time invite for a private partner copy.

     The owner can issue a link during provisioning. A new link invalidates any unredeemed link for the
    copy.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxPartnerInviteResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APISandboxPartnerInviteResponse | Problem]:
    """Issue a one-time invite for a private partner copy.

     The owner can issue a link during provisioning. A new link invalidates any unredeemed link for the
    copy.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxPartnerInviteResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APISandboxPartnerInviteResponse | Problem | None:
    """Issue a one-time invite for a private partner copy.

     The owner can issue a link during provisioning. A new link invalidates any unredeemed link for the
    copy.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxPartnerInviteResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            sandbox=sandbox,
            client=client,
        )
    ).parsed

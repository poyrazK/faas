from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_security_policy import OrgSecurityPolicy
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: OrgSecurityPolicy,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/orgs/{slug}/security-policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgSecurityPolicy | Problem | None:
    if response.status_code == 200:
        response_200 = OrgSecurityPolicy.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgSecurityPolicy | Problem]:
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
    body: OrgSecurityPolicy,
) -> Response[OrgSecurityPolicy | Problem]:
    """Require or stop requiring two-factor authentication for members.

     Owner-only (`org.manage_security`) and needs a fresh step-up. While
    `require_mfa` is on, a member without two-factor authentication is
    refused on the organization's routes with `org_mfa_required`, by
    sessions and API keys alike, until they enrol. Turning it on needs
    the caller to have two-factor authentication. Owners and admins
    are emailed when the policy changes.

    Args:
        slug (str):
        body (OrgSecurityPolicy): An organization's security policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgSecurityPolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: OrgSecurityPolicy,
) -> OrgSecurityPolicy | Problem | None:
    """Require or stop requiring two-factor authentication for members.

     Owner-only (`org.manage_security`) and needs a fresh step-up. While
    `require_mfa` is on, a member without two-factor authentication is
    refused on the organization's routes with `org_mfa_required`, by
    sessions and API keys alike, until they enrol. Turning it on needs
    the caller to have two-factor authentication. Owners and admins
    are emailed when the policy changes.

    Args:
        slug (str):
        body (OrgSecurityPolicy): An organization's security policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgSecurityPolicy | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: OrgSecurityPolicy,
) -> Response[OrgSecurityPolicy | Problem]:
    """Require or stop requiring two-factor authentication for members.

     Owner-only (`org.manage_security`) and needs a fresh step-up. While
    `require_mfa` is on, a member without two-factor authentication is
    refused on the organization's routes with `org_mfa_required`, by
    sessions and API keys alike, until they enrol. Turning it on needs
    the caller to have two-factor authentication. Owners and admins
    are emailed when the policy changes.

    Args:
        slug (str):
        body (OrgSecurityPolicy): An organization's security policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgSecurityPolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: OrgSecurityPolicy,
) -> OrgSecurityPolicy | Problem | None:
    """Require or stop requiring two-factor authentication for members.

     Owner-only (`org.manage_security`) and needs a fresh step-up. While
    `require_mfa` is on, a member without two-factor authentication is
    refused on the organization's routes with `org_mfa_required`, by
    sessions and API keys alike, until they enrol. Turning it on needs
    the caller to have two-factor authentication. Owners and admins
    are emailed when the policy changes.

    Args:
        slug (str):
        body (OrgSecurityPolicy): An organization's security policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgSecurityPolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed

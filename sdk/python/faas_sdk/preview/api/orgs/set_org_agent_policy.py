from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_agent_policy_request import OrgAgentPolicyRequest
from ...models.org_agent_policy_response import OrgAgentPolicyResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: OrgAgentPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/orgs/{slug}/agent-policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgAgentPolicyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgAgentPolicyResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgAgentPolicyResponse | Problem]:
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
    body: OrgAgentPolicyRequest,
) -> Response[OrgAgentPolicyResponse | Problem]:
    """Set whether agents may act in an organization.

     Owners and admins only. Agents connecting to the organization get
    only the capabilities the policy allows. Connections the new policy
    no longer allows are revoked in the same request, and `revoked`
    counts them.

    Args:
        slug (str):
        body (OrgAgentPolicyRequest): Whether agents may act in the organization, and the
            capabilities they may get.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgAgentPolicyResponse | Problem]
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
    body: OrgAgentPolicyRequest,
) -> OrgAgentPolicyResponse | Problem | None:
    """Set whether agents may act in an organization.

     Owners and admins only. Agents connecting to the organization get
    only the capabilities the policy allows. Connections the new policy
    no longer allows are revoked in the same request, and `revoked`
    counts them.

    Args:
        slug (str):
        body (OrgAgentPolicyRequest): Whether agents may act in the organization, and the
            capabilities they may get.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgAgentPolicyResponse | Problem
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
    body: OrgAgentPolicyRequest,
) -> Response[OrgAgentPolicyResponse | Problem]:
    """Set whether agents may act in an organization.

     Owners and admins only. Agents connecting to the organization get
    only the capabilities the policy allows. Connections the new policy
    no longer allows are revoked in the same request, and `revoked`
    counts them.

    Args:
        slug (str):
        body (OrgAgentPolicyRequest): Whether agents may act in the organization, and the
            capabilities they may get.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgAgentPolicyResponse | Problem]
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
    body: OrgAgentPolicyRequest,
) -> OrgAgentPolicyResponse | Problem | None:
    """Set whether agents may act in an organization.

     Owners and admins only. Agents connecting to the organization get
    only the capabilities the policy allows. Connections the new policy
    no longer allows are revoked in the same request, and `revoked`
    counts them.

    Args:
        slug (str):
        body (OrgAgentPolicyRequest): Whether agents may act in the organization, and the
            capabilities they may get.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgAgentPolicyResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed

from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_instance_protocol_probe_request import MCPInstanceProtocolProbeRequest
from ...models.mcp_instance_protocol_probe_response import MCPInstanceProtocolProbeResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    deployment: UUID,
    instance: UUID,
    *,
    body: MCPInstanceProtocolProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(mcp_probe_authorization, Unset):
        headers["MCP-Probe-Authorization"] = mcp_probe_authorization

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/{deployment}/instances/{instance}/mcp-probe".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            instance=quote(str(instance), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MCPInstanceProtocolProbeResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MCPInstanceProtocolProbeResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[MCPInstanceProtocolProbeResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    instance: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPInstanceProtocolProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> Response[MCPInstanceProtocolProbeResponse | Problem]:
    """Send one read-only MCP protocol request to an exact replica.

     Requires app read scope and MFA. The platform verifies that the
    instance is RUNNING in the requested deployment, then forwards one
    allowlisted MCP initialize or catalog-list request to the supplied
    literal endpoint path on that exact instance. Tool execution, resource
    reads, prompt rendering, and task operations are rejected. The response
    includes platform-stamped identity and a bounded workload response.
    The optional MCP-Probe-Authorization header carries a workload bearer
    token for this request only; it is not logged or stored.

    Args:
        slug (str):
        deployment (UUID):
        instance (UUID):
        mcp_probe_authorization (str | Unset):
        body (MCPInstanceProtocolProbeRequest): One bounded, read-only MCP JSON-RPC request to an
            exact replica.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MCPInstanceProtocolProbeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        instance=instance,
        body=body,
        mcp_probe_authorization=mcp_probe_authorization,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    instance: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPInstanceProtocolProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> MCPInstanceProtocolProbeResponse | Problem | None:
    """Send one read-only MCP protocol request to an exact replica.

     Requires app read scope and MFA. The platform verifies that the
    instance is RUNNING in the requested deployment, then forwards one
    allowlisted MCP initialize or catalog-list request to the supplied
    literal endpoint path on that exact instance. Tool execution, resource
    reads, prompt rendering, and task operations are rejected. The response
    includes platform-stamped identity and a bounded workload response.
    The optional MCP-Probe-Authorization header carries a workload bearer
    token for this request only; it is not logged or stored.

    Args:
        slug (str):
        deployment (UUID):
        instance (UUID):
        mcp_probe_authorization (str | Unset):
        body (MCPInstanceProtocolProbeRequest): One bounded, read-only MCP JSON-RPC request to an
            exact replica.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MCPInstanceProtocolProbeResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        instance=instance,
        client=client,
        body=body,
        mcp_probe_authorization=mcp_probe_authorization,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    instance: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPInstanceProtocolProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> Response[MCPInstanceProtocolProbeResponse | Problem]:
    """Send one read-only MCP protocol request to an exact replica.

     Requires app read scope and MFA. The platform verifies that the
    instance is RUNNING in the requested deployment, then forwards one
    allowlisted MCP initialize or catalog-list request to the supplied
    literal endpoint path on that exact instance. Tool execution, resource
    reads, prompt rendering, and task operations are rejected. The response
    includes platform-stamped identity and a bounded workload response.
    The optional MCP-Probe-Authorization header carries a workload bearer
    token for this request only; it is not logged or stored.

    Args:
        slug (str):
        deployment (UUID):
        instance (UUID):
        mcp_probe_authorization (str | Unset):
        body (MCPInstanceProtocolProbeRequest): One bounded, read-only MCP JSON-RPC request to an
            exact replica.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MCPInstanceProtocolProbeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        instance=instance,
        body=body,
        mcp_probe_authorization=mcp_probe_authorization,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    instance: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPInstanceProtocolProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> MCPInstanceProtocolProbeResponse | Problem | None:
    """Send one read-only MCP protocol request to an exact replica.

     Requires app read scope and MFA. The platform verifies that the
    instance is RUNNING in the requested deployment, then forwards one
    allowlisted MCP initialize or catalog-list request to the supplied
    literal endpoint path on that exact instance. Tool execution, resource
    reads, prompt rendering, and task operations are rejected. The response
    includes platform-stamped identity and a bounded workload response.
    The optional MCP-Probe-Authorization header carries a workload bearer
    token for this request only; it is not logged or stored.

    Args:
        slug (str):
        deployment (UUID):
        instance (UUID):
        mcp_probe_authorization (str | Unset):
        body (MCPInstanceProtocolProbeRequest): One bounded, read-only MCP JSON-RPC request to an
            exact replica.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MCPInstanceProtocolProbeResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            instance=instance,
            client=client,
            body=body,
            mcp_probe_authorization=mcp_probe_authorization,
        )
    ).parsed

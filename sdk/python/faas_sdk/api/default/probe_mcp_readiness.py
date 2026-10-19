from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_evidence import MCPReadinessEvidence
from ...models.mcp_readiness_probe_request import MCPReadinessProbeRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    body: MCPReadinessProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(mcp_probe_authorization, Unset):
        headers["MCP-Probe-Authorization"] = mcp_probe_authorization

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/mcp-readiness".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessEvidence | None:
    if response.status_code == 200:
        response_200 = MCPReadinessEvidence.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessEvidence]:
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
    body: MCPReadinessProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> Response[Any | MCPReadinessEvidence]:
    """Run a server-side read-only role probe and retain revision-bound readiness evidence

     Requires deploy write scope and applicable MFA. A failed probe is retained with passed=false. Raw
    client observations are never accepted. The endpoint is platform-generated; credentials and response
    bodies are not retained.

    Args:
        id (str):
        mcp_probe_authorization (str | Unset):
        body (MCPReadinessProbeRequest): Role selection and options for a platform-executed
            deployment readiness probe.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessEvidence]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        mcp_probe_authorization=mcp_probe_authorization,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: MCPReadinessProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> Any | MCPReadinessEvidence | None:
    """Run a server-side read-only role probe and retain revision-bound readiness evidence

     Requires deploy write scope and applicable MFA. A failed probe is retained with passed=false. Raw
    client observations are never accepted. The endpoint is platform-generated; credentials and response
    bodies are not retained.

    Args:
        id (str):
        mcp_probe_authorization (str | Unset):
        body (MCPReadinessProbeRequest): Role selection and options for a platform-executed
            deployment readiness probe.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessEvidence
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        mcp_probe_authorization=mcp_probe_authorization,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: MCPReadinessProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> Response[Any | MCPReadinessEvidence]:
    """Run a server-side read-only role probe and retain revision-bound readiness evidence

     Requires deploy write scope and applicable MFA. A failed probe is retained with passed=false. Raw
    client observations are never accepted. The endpoint is platform-generated; credentials and response
    bodies are not retained.

    Args:
        id (str):
        mcp_probe_authorization (str | Unset):
        body (MCPReadinessProbeRequest): Role selection and options for a platform-executed
            deployment readiness probe.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessEvidence]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        mcp_probe_authorization=mcp_probe_authorization,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: MCPReadinessProbeRequest,
    mcp_probe_authorization: str | Unset = UNSET,
) -> Any | MCPReadinessEvidence | None:
    """Run a server-side read-only role probe and retain revision-bound readiness evidence

     Requires deploy write scope and applicable MFA. A failed probe is retained with passed=false. Raw
    client observations are never accepted. The endpoint is platform-generated; credentials and response
    bodies are not retained.

    Args:
        id (str):
        mcp_probe_authorization (str | Unset):
        body (MCPReadinessProbeRequest): Role selection and options for a platform-executed
            deployment readiness probe.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessEvidence
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            mcp_probe_authorization=mcp_probe_authorization,
        )
    ).parsed

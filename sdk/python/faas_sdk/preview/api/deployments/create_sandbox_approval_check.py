from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_approval_check import APISandboxApprovalCheck
from ...models.api_sandbox_comparison_request import APISandboxComparisonRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    check: UUID,
    *,
    body: APISandboxComparisonRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/deployments/{id}/approvals/sandbox-checks/{check}".format(
            id=quote(str(id), safe=""),
            check=quote(str(check), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxApprovalCheck | Problem:
    if response.status_code == 200:
        response_200 = APISandboxApprovalCheck.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = APISandboxApprovalCheck.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxApprovalCheck | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    check: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> Response[APISandboxApprovalCheck | Problem]:
    """Compare the held build with the current serving release in two sandboxes.

     Uses the live baseline and scenario revision returned by the list endpoint. The production release
    must still be waiting before its release command. Reuse the check UUID and original inputs to retry.

    Args:
        id (UUID):
        check (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxApprovalCheck | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        check=check,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    check: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> APISandboxApprovalCheck | Problem | None:
    """Compare the held build with the current serving release in two sandboxes.

     Uses the live baseline and scenario revision returned by the list endpoint. The production release
    must still be waiting before its release command. Reuse the check UUID and original inputs to retry.

    Args:
        id (UUID):
        check (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxApprovalCheck | Problem
    """

    return sync_detailed(
        id=id,
        check=check,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    check: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> Response[APISandboxApprovalCheck | Problem]:
    """Compare the held build with the current serving release in two sandboxes.

     Uses the live baseline and scenario revision returned by the list endpoint. The production release
    must still be waiting before its release command. Reuse the check UUID and original inputs to retry.

    Args:
        id (UUID):
        check (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxApprovalCheck | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        check=check,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    check: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxComparisonRequest,
) -> APISandboxApprovalCheck | Problem | None:
    """Compare the held build with the current serving release in two sandboxes.

     Uses the live baseline and scenario revision returned by the list endpoint. The production release
    must still be waiting before its release command. Reuse the check UUID and original inputs to retry.

    Args:
        id (UUID):
        check (UUID):
        body (APISandboxComparisonRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxApprovalCheck | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            check=check,
            client=client,
            body=body,
        )
    ).parsed

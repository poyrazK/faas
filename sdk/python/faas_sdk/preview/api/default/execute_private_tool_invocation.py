from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.private_tool_invocation_request import PrivateToolInvocationRequest
from ...models.tool_invocation_receipt import ToolInvocationReceipt
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: PrivateToolInvocationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/delegated-tool-invocations/{id}/execute".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ToolInvocationReceipt | None:
    if response.status_code == 200:
        response_200 = ToolInvocationReceipt.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ToolInvocationReceipt]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PrivateToolInvocationRequest,
) -> Response[Any | ToolInvocationReceipt]:
    """Start one exact credential-bound private invocation; native read scope and MFA policy apply

     Requires operator enablement and an active owner-created credential binding. Only a newly created
    native identity launches a bounded worker. Arguments are transient; no provider credential is
    returned or stored in the journal. Replays recover the retained fact without another execution.
    Rotation, deletion, expiry and revocation prevent subsequent dispatch or receipt access. Only the
    native worker may dispatch or report private outcomes.

    Args:
        id (UUID):
        body (PrivateToolInvocationRequest): Private invocation intent plus bounded transient
            argument JSON. No credential value or secret reference is accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolInvocationReceipt]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PrivateToolInvocationRequest,
) -> Any | ToolInvocationReceipt | None:
    """Start one exact credential-bound private invocation; native read scope and MFA policy apply

     Requires operator enablement and an active owner-created credential binding. Only a newly created
    native identity launches a bounded worker. Arguments are transient; no provider credential is
    returned or stored in the journal. Replays recover the retained fact without another execution.
    Rotation, deletion, expiry and revocation prevent subsequent dispatch or receipt access. Only the
    native worker may dispatch or report private outcomes.

    Args:
        id (UUID):
        body (PrivateToolInvocationRequest): Private invocation intent plus bounded transient
            argument JSON. No credential value or secret reference is accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolInvocationReceipt
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PrivateToolInvocationRequest,
) -> Response[Any | ToolInvocationReceipt]:
    """Start one exact credential-bound private invocation; native read scope and MFA policy apply

     Requires operator enablement and an active owner-created credential binding. Only a newly created
    native identity launches a bounded worker. Arguments are transient; no provider credential is
    returned or stored in the journal. Replays recover the retained fact without another execution.
    Rotation, deletion, expiry and revocation prevent subsequent dispatch or receipt access. Only the
    native worker may dispatch or report private outcomes.

    Args:
        id (UUID):
        body (PrivateToolInvocationRequest): Private invocation intent plus bounded transient
            argument JSON. No credential value or secret reference is accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolInvocationReceipt]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PrivateToolInvocationRequest,
) -> Any | ToolInvocationReceipt | None:
    """Start one exact credential-bound private invocation; native read scope and MFA policy apply

     Requires operator enablement and an active owner-created credential binding. Only a newly created
    native identity launches a bounded worker. Arguments are transient; no provider credential is
    returned or stored in the journal. Replays recover the retained fact without another execution.
    Rotation, deletion, expiry and revocation prevent subsequent dispatch or receipt access. Only the
    native worker may dispatch or report private outcomes.

    Args:
        id (UUID):
        body (PrivateToolInvocationRequest): Private invocation intent plus bounded transient
            argument JSON. No credential value or secret reference is accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolInvocationReceipt
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed

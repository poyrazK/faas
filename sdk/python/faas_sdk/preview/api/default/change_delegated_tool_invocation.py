from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.change_delegated_tool_invocation_action import (
    ChangeDelegatedToolInvocationAction,
)
from ...models.change_delegated_tool_invocation_body_type_0 import ChangeDelegatedToolInvocationBodyType0
from ...models.tool_invocation_outcome import ToolInvocationOutcome
from ...models.tool_invocation_receipt import ToolInvocationReceipt
from ...types import Response


def _get_kwargs(
    id: UUID,
    action: ChangeDelegatedToolInvocationAction,
    *,
    body: ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/delegated-tool-invocations/{id}/{action}".format(
            id=quote(str(id), safe=""),
            action=quote(str(action), safe=""),
        ),
    }

    if isinstance(body, ChangeDelegatedToolInvocationBodyType0):
        _kwargs["json"] = body.to_dict()
    else:
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
    action: ChangeDelegatedToolInvocationAction,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome,
) -> Response[Any | ToolInvocationReceipt]:
    """Dispatch, cancel or finish a call through its frozen invocation grant

     Requires the original personal agent key, the same active invocation grant and native MFA policy.
    Dispatch locks the current grant, key, app and journal row before granting only the first queued-to-
    running transition. Lost dispatch replies are never replayed as permission. Cancellation after
    dispatch requests stopping the worker wait without rolling back remote effects. Outcomes are
    immutable; unknown outcomes never authorize retry.

    Args:
        id (UUID):
        action (ChangeDelegatedToolInvocationAction):
        body (ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolInvocationReceipt]
    """

    kwargs = _get_kwargs(
        id=id,
        action=action,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    action: ChangeDelegatedToolInvocationAction,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome,
) -> Any | ToolInvocationReceipt | None:
    """Dispatch, cancel or finish a call through its frozen invocation grant

     Requires the original personal agent key, the same active invocation grant and native MFA policy.
    Dispatch locks the current grant, key, app and journal row before granting only the first queued-to-
    running transition. Lost dispatch replies are never replayed as permission. Cancellation after
    dispatch requests stopping the worker wait without rolling back remote effects. Outcomes are
    immutable; unknown outcomes never authorize retry.

    Args:
        id (UUID):
        action (ChangeDelegatedToolInvocationAction):
        body (ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolInvocationReceipt
    """

    return sync_detailed(
        id=id,
        action=action,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    action: ChangeDelegatedToolInvocationAction,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome,
) -> Response[Any | ToolInvocationReceipt]:
    """Dispatch, cancel or finish a call through its frozen invocation grant

     Requires the original personal agent key, the same active invocation grant and native MFA policy.
    Dispatch locks the current grant, key, app and journal row before granting only the first queued-to-
    running transition. Lost dispatch replies are never replayed as permission. Cancellation after
    dispatch requests stopping the worker wait without rolling back remote effects. Outcomes are
    immutable; unknown outcomes never authorize retry.

    Args:
        id (UUID):
        action (ChangeDelegatedToolInvocationAction):
        body (ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolInvocationReceipt]
    """

    kwargs = _get_kwargs(
        id=id,
        action=action,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    action: ChangeDelegatedToolInvocationAction,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome,
) -> Any | ToolInvocationReceipt | None:
    """Dispatch, cancel or finish a call through its frozen invocation grant

     Requires the original personal agent key, the same active invocation grant and native MFA policy.
    Dispatch locks the current grant, key, app and journal row before granting only the first queued-to-
    running transition. Lost dispatch replies are never replayed as permission. Cancellation after
    dispatch requests stopping the worker wait without rolling back remote effects. Outcomes are
    immutable; unknown outcomes never authorize retry.

    Args:
        id (UUID):
        action (ChangeDelegatedToolInvocationAction):
        body (ChangeDelegatedToolInvocationBodyType0 | ToolInvocationOutcome):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolInvocationReceipt
    """

    return (
        await asyncio_detailed(
            id=id,
            action=action,
            client=client,
            body=body,
        )
    ).parsed

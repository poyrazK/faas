from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.complete_workflow_callback_response import CompleteWorkflowCallbackResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    callback_id: UUID,
    *,
    body: Any | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/workflows/runs/{id}/callbacks/{callback_id}".format(
            id=quote(str(id), safe=""),
            callback_id=quote(str(callback_id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CompleteWorkflowCallbackResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CompleteWorkflowCallbackResponse.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
) -> Response[CompleteWorkflowCallbackResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Response[CompleteWorkflowCallbackResponse | Problem]:
    """Complete one callback wait.

     Requires the run owner's workflow-write authorization. An identical
    retry returns duplicate=true, including after the run finishes; a
    different payload conflicts. Completion may arrive before the step
    parks and will be consumed when the step becomes runnable.

    Args:
        id (UUID):
        callback_id (UUID):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CompleteWorkflowCallbackResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        callback_id=callback_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> CompleteWorkflowCallbackResponse | Problem | None:
    """Complete one callback wait.

     Requires the run owner's workflow-write authorization. An identical
    retry returns duplicate=true, including after the run finishes; a
    different payload conflicts. Completion may arrive before the step
    parks and will be consumed when the step becomes runnable.

    Args:
        id (UUID):
        callback_id (UUID):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CompleteWorkflowCallbackResponse | Problem
    """

    return sync_detailed(
        id=id,
        callback_id=callback_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Response[CompleteWorkflowCallbackResponse | Problem]:
    """Complete one callback wait.

     Requires the run owner's workflow-write authorization. An identical
    retry returns duplicate=true, including after the run finishes; a
    different payload conflicts. Completion may arrive before the step
    parks and will be consumed when the step becomes runnable.

    Args:
        id (UUID):
        callback_id (UUID):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CompleteWorkflowCallbackResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        callback_id=callback_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> CompleteWorkflowCallbackResponse | Problem | None:
    """Complete one callback wait.

     Requires the run owner's workflow-write authorization. An identical
    retry returns duplicate=true, including after the run finishes; a
    different payload conflicts. Completion may arrive before the step
    parks and will be consumed when the step becomes runnable.

    Args:
        id (UUID):
        callback_id (UUID):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CompleteWorkflowCallbackResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            callback_id=callback_id,
            client=client,
            body=body,
        )
    ).parsed

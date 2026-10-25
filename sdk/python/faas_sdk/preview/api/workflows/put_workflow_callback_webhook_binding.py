from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_workflow_callback_webhook_binding_request import CreateWorkflowCallbackWebhookBindingRequest
from ...models.problem import Problem
from ...models.workflow_callback_webhook_binding_response import WorkflowCallbackWebhookBindingResponse
from ...types import Response


def _get_kwargs(
    id: UUID,
    callback_id: UUID,
    *,
    body: CreateWorkflowCallbackWebhookBindingRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding".format(
            id=quote(str(id), safe=""),
            callback_id=quote(str(callback_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowCallbackWebhookBindingResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowCallbackWebhookBindingResponse.from_dict(response.json())

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
) -> Response[Problem | WorkflowCallbackWebhookBindingResponse]:
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
    body: CreateWorkflowCallbackWebhookBindingRequest,
) -> Response[Problem | WorkflowCallbackWebhookBindingResponse]:
    """Bind one verified Stripe object event to a workflow callback.

     Requires account workflow-write authorization and a Stripe inbound
    webhook endpoint owned by the same app. Repeating the identical
    binding is safe; another callback cannot claim the same endpoint,
    event type, and object ID. The endpoint's existing URL and signing
    secret are reused.

    Args:
        id (UUID):
        callback_id (UUID):
        body (CreateWorkflowCallbackWebhookBindingRequest): Exact Stripe event/object correlation
            for one callback on an existing signed inbound endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowCallbackWebhookBindingResponse]
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
    body: CreateWorkflowCallbackWebhookBindingRequest,
) -> Problem | WorkflowCallbackWebhookBindingResponse | None:
    """Bind one verified Stripe object event to a workflow callback.

     Requires account workflow-write authorization and a Stripe inbound
    webhook endpoint owned by the same app. Repeating the identical
    binding is safe; another callback cannot claim the same endpoint,
    event type, and object ID. The endpoint's existing URL and signing
    secret are reused.

    Args:
        id (UUID):
        callback_id (UUID):
        body (CreateWorkflowCallbackWebhookBindingRequest): Exact Stripe event/object correlation
            for one callback on an existing signed inbound endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowCallbackWebhookBindingResponse
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
    body: CreateWorkflowCallbackWebhookBindingRequest,
) -> Response[Problem | WorkflowCallbackWebhookBindingResponse]:
    """Bind one verified Stripe object event to a workflow callback.

     Requires account workflow-write authorization and a Stripe inbound
    webhook endpoint owned by the same app. Repeating the identical
    binding is safe; another callback cannot claim the same endpoint,
    event type, and object ID. The endpoint's existing URL and signing
    secret are reused.

    Args:
        id (UUID):
        callback_id (UUID):
        body (CreateWorkflowCallbackWebhookBindingRequest): Exact Stripe event/object correlation
            for one callback on an existing signed inbound endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowCallbackWebhookBindingResponse]
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
    body: CreateWorkflowCallbackWebhookBindingRequest,
) -> Problem | WorkflowCallbackWebhookBindingResponse | None:
    """Bind one verified Stripe object event to a workflow callback.

     Requires account workflow-write authorization and a Stripe inbound
    webhook endpoint owned by the same app. Repeating the identical
    binding is safe; another callback cannot claim the same endpoint,
    event type, and object ID. The endpoint's existing URL and signing
    secret are reused.

    Args:
        id (UUID):
        callback_id (UUID):
        body (CreateWorkflowCallbackWebhookBindingRequest): Exact Stripe event/object correlation
            for one callback on an existing signed inbound endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowCallbackWebhookBindingResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            callback_id=callback_id,
            client=client,
            body=body,
        )
    ).parsed

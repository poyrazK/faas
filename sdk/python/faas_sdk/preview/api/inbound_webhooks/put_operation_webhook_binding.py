from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_webhook_binding_response import OperationWebhookBindingResponse
from ...models.problem import Problem
from ...models.put_operation_webhook_binding_request import PutOperationWebhookBindingRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: PutOperationWebhookBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/inbound-webhooks/{id}/operation-binding".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWebhookBindingResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWebhookBindingResponse.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[OperationWebhookBindingResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOperationWebhookBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[OperationWebhookBindingResponse | Problem]:
    """Route a verified endpoint to a customer operation.

     One binding per endpoint, owned by the same app. The platform tenant is
    the operation owner; it must be active and linked to the app through an
    active tenant surface, and it is never derived from provider payload.
    The named operation must exist on the environment's live deployment;
    every event resolves it again and admission pins it. expected_version is
    zero for creation and the returned version for updates.
    take_over_delivery must be true. Automation and managed exclusive
    operation bindings must be removed first. An empty event_types list
    admits every verified event; other types are acknowledged as ignored.

    mode signal starts nothing. Each event whose type a signals
    entry maps is delivered as that operation event to the platform
    tenant's active operations of the named definition whose subject
    matches subject.id_from in the provider payload, at most 16 per event.
    The subject type must equal the definition's subject type, the
    definition must be a workflow, and its live workflow must wait for
    every mapped event. event_types must be empty in this mode.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (PutOperationWebhookBindingRequest): Bind a verified provider endpoint to a customer
            operation with revision control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWebhookBindingResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOperationWebhookBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> OperationWebhookBindingResponse | Problem | None:
    """Route a verified endpoint to a customer operation.

     One binding per endpoint, owned by the same app. The platform tenant is
    the operation owner; it must be active and linked to the app through an
    active tenant surface, and it is never derived from provider payload.
    The named operation must exist on the environment's live deployment;
    every event resolves it again and admission pins it. expected_version is
    zero for creation and the returned version for updates.
    take_over_delivery must be true. Automation and managed exclusive
    operation bindings must be removed first. An empty event_types list
    admits every verified event; other types are acknowledged as ignored.

    mode signal starts nothing. Each event whose type a signals
    entry maps is delivered as that operation event to the platform
    tenant's active operations of the named definition whose subject
    matches subject.id_from in the provider payload, at most 16 per event.
    The subject type must equal the definition's subject type, the
    definition must be a workflow, and its live workflow must wait for
    every mapped event. event_types must be empty in this mode.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (PutOperationWebhookBindingRequest): Bind a verified provider endpoint to a customer
            operation with revision control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWebhookBindingResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOperationWebhookBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[OperationWebhookBindingResponse | Problem]:
    """Route a verified endpoint to a customer operation.

     One binding per endpoint, owned by the same app. The platform tenant is
    the operation owner; it must be active and linked to the app through an
    active tenant surface, and it is never derived from provider payload.
    The named operation must exist on the environment's live deployment;
    every event resolves it again and admission pins it. expected_version is
    zero for creation and the returned version for updates.
    take_over_delivery must be true. Automation and managed exclusive
    operation bindings must be removed first. An empty event_types list
    admits every verified event; other types are acknowledged as ignored.

    mode signal starts nothing. Each event whose type a signals
    entry maps is delivered as that operation event to the platform
    tenant's active operations of the named definition whose subject
    matches subject.id_from in the provider payload, at most 16 per event.
    The subject type must equal the definition's subject type, the
    definition must be a workflow, and its live workflow must wait for
    every mapped event. event_types must be empty in this mode.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (PutOperationWebhookBindingRequest): Bind a verified provider endpoint to a customer
            operation with revision control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWebhookBindingResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOperationWebhookBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> OperationWebhookBindingResponse | Problem | None:
    """Route a verified endpoint to a customer operation.

     One binding per endpoint, owned by the same app. The platform tenant is
    the operation owner; it must be active and linked to the app through an
    active tenant surface, and it is never derived from provider payload.
    The named operation must exist on the environment's live deployment;
    every event resolves it again and admission pins it. expected_version is
    zero for creation and the returned version for updates.
    take_over_delivery must be true. Automation and managed exclusive
    operation bindings must be removed first. An empty event_types list
    admits every verified event; other types are acknowledged as ignored.

    mode signal starts nothing. Each event whose type a signals
    entry maps is delivered as that operation event to the platform
    tenant's active operations of the named definition whose subject
    matches subject.id_from in the provider payload, at most 16 per event.
    The subject type must equal the definition's subject type, the
    definition must be a workflow, and its live workflow must wait for
    every mapped event. event_types must be empty in this mode.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (PutOperationWebhookBindingRequest): Bind a verified provider endpoint to a customer
            operation with revision control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWebhookBindingResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed

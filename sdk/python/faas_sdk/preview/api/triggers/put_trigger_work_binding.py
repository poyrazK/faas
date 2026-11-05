from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.trigger_work_binding import TriggerWorkBinding
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    body: TriggerWorkBinding,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/triggers/{id}/work-binding".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TriggerWorkBinding | None:
    if response.status_code == 200:
        response_200 = TriggerWorkBinding.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | TriggerWorkBinding]:
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
    body: TriggerWorkBinding,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TriggerWorkBinding]:
    """Set the broker trigger's application work-policy binding.

     An initial binding requires a disabled external trigger with no
    existing record receipts. Resume the trigger after binding it.
    Updating an existing binding is allowed while the trigger is enabled;
    records already admitted keep their captured policy and key.

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (TriggerWorkBinding): An external broker trigger's binding to a named app work
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TriggerWorkBinding]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TriggerWorkBinding,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TriggerWorkBinding | None:
    """Set the broker trigger's application work-policy binding.

     An initial binding requires a disabled external trigger with no
    existing record receipts. Resume the trigger after binding it.
    Updating an existing binding is allowed while the trigger is enabled;
    records already admitted keep their captured policy and key.

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (TriggerWorkBinding): An external broker trigger's binding to a named app work
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TriggerWorkBinding
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TriggerWorkBinding,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TriggerWorkBinding]:
    """Set the broker trigger's application work-policy binding.

     An initial binding requires a disabled external trigger with no
    existing record receipts. Resume the trigger after binding it.
    Updating an existing binding is allowed while the trigger is enabled;
    records already admitted keep their captured policy and key.

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (TriggerWorkBinding): An external broker trigger's binding to a named app work
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TriggerWorkBinding]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TriggerWorkBinding,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TriggerWorkBinding | None:
    """Set the broker trigger's application work-policy binding.

     An initial binding requires a disabled external trigger with no
    existing record receipts. Resume the trigger after binding it.
    Updating an existing binding is allowed while the trigger is enabled;
    records already admitted keep their captured policy and key.

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (TriggerWorkBinding): An external broker trigger's binding to a named app work
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TriggerWorkBinding
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed

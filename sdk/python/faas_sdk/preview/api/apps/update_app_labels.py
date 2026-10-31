from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.label_change import LabelChange
from ...models.labels_response import LabelsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: LabelChange,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/labels".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> LabelsResponse | Problem:
    if response.status_code == 200:
        response_200 = LabelsResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[LabelsResponse | Problem]:
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
    body: LabelChange,
) -> Response[LabelsResponse | Problem]:
    """Set and remove an app's labels.

     Preview (resource-labels capability). Sets and removes labels in one
    atomic write and returns every label the app carries after it. The
    write is idempotent. A result over 32 labels is refused with
    label_limit_exceeded; an invalid key or value with invalid_label_key
    or invalid_label_value; a key under gregale.dev/ with
    reserved_label_key. Emits a resource.labels.changed audit event.
    Requires apps:deploy or admin scope and MFA.

    Args:
        slug (str):
        body (LabelChange): Labels to set and keys to remove, applied together. Setting a key to
            its current value and removing an absent key change nothing. A key may not be both set and
            removed, and keys under gregale.dev/ cannot be changed. Example: {'set': {'team':
            'billing'}, 'remove': ['tier']}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LabelsResponse | Problem]
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
    body: LabelChange,
) -> LabelsResponse | Problem | None:
    """Set and remove an app's labels.

     Preview (resource-labels capability). Sets and removes labels in one
    atomic write and returns every label the app carries after it. The
    write is idempotent. A result over 32 labels is refused with
    label_limit_exceeded; an invalid key or value with invalid_label_key
    or invalid_label_value; a key under gregale.dev/ with
    reserved_label_key. Emits a resource.labels.changed audit event.
    Requires apps:deploy or admin scope and MFA.

    Args:
        slug (str):
        body (LabelChange): Labels to set and keys to remove, applied together. Setting a key to
            its current value and removing an absent key change nothing. A key may not be both set and
            removed, and keys under gregale.dev/ cannot be changed. Example: {'set': {'team':
            'billing'}, 'remove': ['tier']}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LabelsResponse | Problem
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
    body: LabelChange,
) -> Response[LabelsResponse | Problem]:
    """Set and remove an app's labels.

     Preview (resource-labels capability). Sets and removes labels in one
    atomic write and returns every label the app carries after it. The
    write is idempotent. A result over 32 labels is refused with
    label_limit_exceeded; an invalid key or value with invalid_label_key
    or invalid_label_value; a key under gregale.dev/ with
    reserved_label_key. Emits a resource.labels.changed audit event.
    Requires apps:deploy or admin scope and MFA.

    Args:
        slug (str):
        body (LabelChange): Labels to set and keys to remove, applied together. Setting a key to
            its current value and removing an absent key change nothing. A key may not be both set and
            removed, and keys under gregale.dev/ cannot be changed. Example: {'set': {'team':
            'billing'}, 'remove': ['tier']}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LabelsResponse | Problem]
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
    body: LabelChange,
) -> LabelsResponse | Problem | None:
    """Set and remove an app's labels.

     Preview (resource-labels capability). Sets and removes labels in one
    atomic write and returns every label the app carries after it. The
    write is idempotent. A result over 32 labels is refused with
    label_limit_exceeded; an invalid key or value with invalid_label_key
    or invalid_label_value; a key under gregale.dev/ with
    reserved_label_key. Emits a resource.labels.changed audit event.
    Requires apps:deploy or admin scope and MFA.

    Args:
        slug (str):
        body (LabelChange): Labels to set and keys to remove, applied together. Setting a key to
            its current value and removing an absent key change nothing. A key may not be both set and
            removed, and keys under gregale.dev/ cannot be changed. Example: {'set': {'team':
            'billing'}, 'remove': ['tier']}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LabelsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed

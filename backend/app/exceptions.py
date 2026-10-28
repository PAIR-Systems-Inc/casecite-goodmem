"""
Domain exceptions for the HTTP layer.

Routers raise one of these instead of hand-building ``HTTPException`` with a
literal status code. ``app.error_handlers`` turns an ``AppError`` into exactly
the JSON body and status that an ``HTTPException`` with the same fields
produces, so clients see no difference between the two.
"""


class AppError(Exception):
    """Base class: an error with a fixed HTTP status and a client-facing detail."""

    status_code: int = 500

    def __init__(
        self,
        detail: str | None = None,
        *,
        status_code: int | None = None,
        headers: dict[str, str] | None = None,
    ) -> None:
        if status_code is not None:
            self.status_code = status_code
        self.detail = detail
        self.headers = headers
        super().__init__(detail)

    def __repr__(self) -> str:
        return f"{type(self).__name__}(status_code={self.status_code!r}, detail={self.detail!r})"


class BadRequestError(AppError):
    status_code = 400


class UnauthorizedError(AppError):
    status_code = 401


class ForbiddenError(AppError):
    status_code = 403


class NotFoundError(AppError):
    status_code = 404


class ConflictError(AppError):
    status_code = 409


class UnprocessableError(AppError):
    status_code = 422


class RateLimitedError(AppError):
    status_code = 429


class UpstreamError(AppError):
    status_code = 502


class ServiceUnavailableError(AppError):
    status_code = 503


__all__ = [
    "AppError",
    "BadRequestError",
    "ConflictError",
    "ForbiddenError",
    "NotFoundError",
    "RateLimitedError",
    "ServiceUnavailableError",
    "UnauthorizedError",
    "UnprocessableError",
    "UpstreamError",
]

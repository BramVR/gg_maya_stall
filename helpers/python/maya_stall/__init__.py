"""Tiny helpers for Maya Stall Scenario Result JSON."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, Mapping, MutableMapping, Optional, Union

RESULT_ENV_VAR = "MAYA_STALL_SCENARIO_RESULT"


class ResultPathError(RuntimeError):
    """Raised when Maya Stall did not provide a Scenario Result path."""


def result_path(env: Optional[Mapping[str, str]] = None) -> str:
    values = os.environ if env is None else env
    path = values.get(RESULT_ENV_VAR, "")
    if not path:
        raise ResultPathError(f"{RESULT_ENV_VAR} is not set")
    return path


def write_result(
    status: str = "passed",
    summary: Optional[str] = None,
    path: Optional[Union[str, os.PathLike]] = None,
    env: Optional[Mapping[str, str]] = None,
    **fields: Any,
) -> str:
    if not status:
        raise ValueError("status must not be empty")
    target = Path(path if path is not None else result_path(env))
    result: MutableMapping[str, Any] = {"status": status}
    if summary:
        result["summary"] = summary
    result.update(fields)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    return str(target)


def write_passed(
    summary: Optional[str] = None,
    path: Optional[Union[str, os.PathLike]] = None,
    env: Optional[Mapping[str, str]] = None,
    **fields: Any,
) -> str:
    return write_result("passed", summary=summary, path=path, env=env, **fields)


def write_failed(
    summary: Optional[str] = None,
    path: Optional[Union[str, os.PathLike]] = None,
    env: Optional[Mapping[str, str]] = None,
    **fields: Any,
) -> str:
    return write_result("failed", summary=summary, path=path, env=env, **fields)


__all__ = [
    "RESULT_ENV_VAR",
    "ResultPathError",
    "result_path",
    "write_failed",
    "write_passed",
    "write_result",
]

i3d Starlark API
================

Predeclared globals
-------------------

- ``i3`` module (see below)
- ``pid`` module (see below)
- ``exec(args: list[str], check: bool=True, capture_stdout: bool=True, capture_stderr: bool=True) -> dict``
  Returns ``{"rc": int, "stdout": str|None, "stderr": str|None}``. If ``check=True`` and ``rc != 0`` => raises an error.
- ``log(msg: str)`` writes to stderr.
- ``debug: bool`` reflects ``DEBUG=1``.
- ``__file__: str`` current script path.

Event handlers (optional)
-------------------------

Scripts can define any of the following functions to handle i3 events:

- ``on_workspace(e)``
- ``on_output(e)``
- ``on_mode(e)``
- ``on_window(e)``
- ``on_barconfig_update(e)``
- ``on_binding(e)``

Event object
------------

All handlers receive a dict with at least:

- ``{"type": str, "change": str}``

For ``on_window`` events, the daemon also adds:

- ``con_id: int|None``
- ``workspace_num: int|None``
- ``fullscreen_mode: int|None``

i3 module
---------

- ``i3.command(cmd: str) -> bool``
- ``i3.raw(msg: str, payload: str="") -> str`` (raw JSON)
- ``i3.query(msg: str, payload: str="") -> value`` (JSON -> Starlark)
- ``i3.find(criteria: dict, fields: list[str]=[], limit: int=0) -> list[dict]``
- ``i3.set_urgency(con_id: int, urgent: bool=True) -> bool``
- ``i3.rename_workspace(old: str|int, new: str) -> bool``
- ``i3.rename_current_workspace(new: str) -> bool``
- ``i3.get_workspace_names() -> list[str]``
- ``i3.get_tree() -> dict/list``
- ``i3.get_workspaces() -> list``
- ``i3.get_outputs() -> list``
- ``i3.get_marks() -> list[str]``
- ``i3.get_version() -> dict``
- ``i3.get_bar_ids() -> list``
- ``i3.get_bar_config(bar_id: str) -> dict``
- ``i3.get_window_pid(con_id: int) -> int|None``

pid module
----------

- ``pid.is_ancestor(ancestor: int, descendant: int) -> bool``
- ``pid.watch_new(callback: function, poll_interval_ms: int=100, use_poll: bool=False) -> function``
  Returns a stop function that cancels the watcher.

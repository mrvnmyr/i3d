i3d
===

A small, fast i3wm daemon that embeds Starlark scripts.

- Watches: ``$HOME/.config/i3d/*.starlark`` (override with ``I3D_DIR=/path``)
- Reloads scripts on add/change/remove (debounced).
- Scripts register i3 event handlers by defining functions:

  - ``on_workspace(e)``
  - ``on_output(e)``
  - ``on_mode(e)``
  - ``on_window(e)``
  - ``on_barconfig_update(e)``
  - ``on_binding(e)``

- If multiple scripts handle the same event, execution order is by:

  1) higher ``priority`` first
  2) then by filename (stable/deterministic)

Debug logging
-------------

Set ``DEBUG=1`` to enable daemon debug logs. Script ``print(...)`` always prints.

Run
---

.. code:: bash

   ./run.sh

Build
-----

.. code:: bash

   ./build.sh

Starlark environment
--------------------

Predeclared globals for every script:

- ``i3``: module with:

  - ``i3.command(cmd: str) -> bool``
  - ``i3.raw(msg: str, payload: str="") -> str`` (raw JSON)
  - ``i3.query(msg: str, payload: str="") -> value`` (JSON -> Starlark)
- ``i3.find(criteria: dict) -> list[dict]``
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

- ``exec(args: list[str], check: bool=True, capture_stdout: bool=True, capture_stderr: bool=True) -> dict`` Returns: ``{"rc": int, "stdout": str|None, "stderr": str|None}`` If ``check=True`` and rc != 0 => raises an error.
- ``log(msg: str)`` writes to stderr.
- ``debug: bool`` reflects ``DEBUG=1``.
- ``__file__: str`` current script path.

Notes
-----

- The embedded i3 event object is: ``{"type": "...", "change": "..."}``. (i3ipc-go’s Event struct is minimal; scripts can query i3 state via ``i3.*``.)

# 017 Agent Ergonomics Requirements

`afc-145` is the execution issue for this packet. Registration and issue
discovery must be usable without reading CLI source.

- Every CLI leaf accepts `--help` locally and displays required positionals,
  required flags, and optional flags. Invalid input points to that help.
- The root command without arguments and command groups such as `project`
  display local usage. `--help` and `-help` work at both levels, group help
  lists subcommands, and the common `projects` typo suggests `project`.
- Project and repository selectors accept keys/logical names or IDs wherever
  the corresponding CLI/API filter accepts a selector.
- JSON registration output exposes a top-level `id` consistently; existing
  response fields remain available for compatibility.
- Issue lists implement bounded `limit` and `offset` in the daemon, after
  filtering and stable ordering. Ready issue MCP results include project keys.
- MCP documents actor requirements and exposes bounded discovery operations.
  Other mutations remain available through the documented CLI lifecycle.

Existing lease fencing, daemon ownership, and operation replay rules apply.

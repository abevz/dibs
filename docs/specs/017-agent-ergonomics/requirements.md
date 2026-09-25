# 017 Agent Ergonomics Requirements

`afc-145` is the execution issue for this packet. Registration and issue
discovery must be usable without reading CLI source.

- Every CLI leaf accepts `--help` locally and displays required positionals,
  required flags, and optional flags. Invalid input points to that help.
- The root command without arguments and command groups such as `project`
  display local usage. `--help` and `-help` work at both levels, group help
  lists subcommands, and the common `projects` typo suggests `project`.
- Group help gives a concise purpose for every listed subcommand, including
  nested `issue` groups and the top-level `dependency` alias. Descriptions
  remain accurate when routed subcommands are added.
- Root and leaf help use the same command-purpose catalog as group help, so
  every routed command is discoverable and described without contacting the
  daemon. The catalog lives outside the dispatch code and is compiled into
  the CLI; all routed paths have coverage tests.
- Unknown commands and flags show a local `--help` path for the nearest known
  command or group. Leaf help explains the meaning and expected value of every
  routed flag, marks required flags, and gives a concrete project-add example.
  Arguments after `issue run --` remain child arguments.
- Project and repository selectors accept keys/logical names or IDs wherever
  the corresponding CLI/API filter accepts a selector.
- JSON registration output exposes a top-level `id` consistently; existing
  response fields remain available for compatibility.
- Issue lists implement bounded `limit` and `offset` in the daemon, after
  filtering and stable ordering. Ready issue MCP results include project keys.
- MCP documents actor requirements and exposes bounded discovery operations.
  Other mutations remain available through the documented CLI lifecycle.

Existing lease fencing, daemon ownership, and operation replay rules apply.

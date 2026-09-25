# 017 Agent Ergonomics Design

Reuse the CLI route table to render local leaf help before daemon contact.
Render root and group help before leaf validation; derive group subcommands
from the same route table. Keep unknown commands as errors with a specific
suggestion for `projects`.
For afc-147, derive the nearest help path from known route/group prefixes and
render leaf flag explanations from a shared metadata table, with command-specific
entries where the same flag has different semantics. A coverage test requires
metadata for every routed flag; secret lease-token values never appear in usage.
Resolve project IDs and keys at the store boundary. Apply issue pagination in
the SQLite query so filtering and ordering remain authoritative. MCP discovery
uses existing client API calls and applies an explicit bounded page contract;
mutating MCP expansion is deferred to a separately reviewed lifecycle design.

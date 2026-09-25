# 017 Agent Ergonomics Design

Reuse the CLI route table to render local leaf help before daemon contact.
Render root and group help before leaf validation; derive group subcommands
from the same route table. Keep unknown commands as errors with a specific
suggestion for `projects`.
Resolve project IDs and keys at the store boundary. Apply issue pagination in
the SQLite query so filtering and ordering remain authoritative. MCP discovery
uses existing client API calls and applies an explicit bounded page contract;
mutating MCP expansion is deferred to a separately reviewed lifecycle design.

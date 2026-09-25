# 017 Agent Ergonomics Design

Reuse the CLI route table to render local leaf help before daemon contact.
Resolve project IDs and keys at the store boundary. Apply issue pagination in
the SQLite query so filtering and ordering remain authoritative. MCP discovery
uses existing client API calls and applies an explicit bounded page contract;
mutating MCP expansion is deferred to a separately reviewed lifecycle design.

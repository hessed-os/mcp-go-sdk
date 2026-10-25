# hessed-os/mcp-go-sdk

A fork of [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) used by
[Sillok](https://github.com/hessed-os/sillok). It exists because Sillok needs two server features
that upstream v1.8.0 does not have yet, and waiting for them would stall Sillok's release:

- the MCP events extension (`events/list`, `events/subscribe`, `events/unsubscribe` and the
  `events` server capability), which ChatGPT uses to wake an MCP server's subscribers;
- a token check given to the Streamable HTTP handler as an option, so the handler itself answers
  401 with the OAuth discovery header and passes the token info to tool calls.

Rules for this fork:

- The `sillok` branch starts at upstream tag `v1.8.0`. The module path is
  `github.com/hessed-os/mcp-go-sdk` so that `go install <module>@<version>` of programs that use it
  keeps working; a `replace` directive would break that.
- One commit per patch. Every patch is also offered upstream as a pull request. When upstream ships
  an equivalent feature, the patch is dropped, and when nothing is left the fork is retired and
  Sillok goes back to the upstream module.
- Tags are `v<upstream version>-sillok.<n>`.
- The license is upstream's, unchanged.

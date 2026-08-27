# cozy-tools

Shared tool implementations for agent harnesses and MCP servers.

`cozy-tools` owns tool contracts, schemas, handlers, renderers, security metadata, and reusable runtimes. Applications such as `cozyphi` and `mcp-ai-helper` provide host capabilities and register catalogs from this module instead of maintaining their own tools.

The module is being built in vertical slices. Each slice is tested here before replacing the corresponding implementations in its consumers.

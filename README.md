# Memory MCP Server

MCP server for a personal memory database, using libSQL (converting from a file-based memory MCP). 

Exposes `query` (SELECT) and `execute` (INSERT/UPDATE/DELETE) tools for raw SQL access.

## Run

Requires a libSQL server:

```bash
docker run -d --name libsql -p 8080:8080 -v memory-data:/var/lib/sqld ghcr.io/tursodatabase/libsql-server:latest
```

Then either build locally or use Docker:

```bash
# local
go build -o memory-mcp . && LIBSQL_URL=http://localhost:8080 ./memory-mcp

# docker
docker build -t memory-mcp . && docker run --rm memory-mcp
```

## Claude Desktop

```json
{
  "mcpServers": {
    "memory": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "--add-host=host.docker.internal:host-gateway",
        "-e", "LIBSQL_URL=http://host.docker.internal:8080",
        "memory-mcp"
      ]
    }
  }
}
```

### Claude
This was vibe coded by Claude.

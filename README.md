# pg-mcp

A PostgreSQL [MCP (Model Context Protocol)](https://github.com/mark3labs/mcp-go) server built in Go.  
This server provides a safe interface for querying PostgreSQL databases with **read-only** access.  

It exposes MCP tools for:  
- Listing tables  
- Describing tables  
- Executing **safe** `SELECT` or `WITH` queries  

Write operations (`INSERT`, `UPDATE`, `DELETE`, `DROP`, etc.) are **blocked** by default.

---

## Features

- Safe query execution (only `SELECT` and `WITH` queries allowed)  
- Protection against destructive SQL (DROP, DELETE, TRUNCATE, ALTER, etc.)  
- Schema discovery when queries fail  
- Two transport modes:
  - **stdio** (default) for CLI/agent integration
  - **http** for HTTP-based usage
- Docker-ready

---

## Installation

### Clone and Build

```bash
git clone https://github.com/root27/pg-mcp.git
cd pg-mcp
go build -o pg-mcp

```

## Configuration

Database connection is configured using environment variables:

| Variable      | Default     | Description                |
|---------------|-------------|----------------------------|
| `DB_HOST`     | `localhost` | Database host              |
| `DB_PORT`     | `5432`      | Database port              |
| `DB_USER`     | `postgres`  | Database user              |
| `DB_PASSWORD` | `password`  | Database password          |
| `DB_NAME`     | `mydb`      | Database name              |
| `DB_SSLMODE`  | `disable`   | SSL mode (e.g. `require`)  |

Example:
```bash
export DB_HOST=localhost
export DB_PORT=5432
export DB_USER=postgres
export DB_PASSWORD=secret
export DB_NAME=mydb
export DB_SSLMODE=disable
```
## Running the server

### Run (stdio transport)

This is the default mode. It runs over **stdio**, which is useful for direct MCP integrations (e.g., when an agent launches the server as a subprocess).

```bash

./pg-mcp

```

### Run (http transport)

You can also run the server over **HTTP**, which is useful if you want to expose it as a service.


```bash

./pg-mcp -t http

# or

./pg-mcp --transport http

```


The server will listen on 

```bash

http://localhost:8080/mcp

```

## Docker

You can pull the images for arm64 and amd64 

### Arm64

```bash

docker pull ghcr.io/root27/pg-mcp:arm64

```

#### Run Container


```bash

docker run -it  \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=5432 \
  -e DB_USER=postgres \
  -e DB_PASSWORD=secret \
  -e DB_NAME=mydb \
  -e DB_SSLMODE=disable \
  -p 8080:8080 \
  ghcr.io/root27/pg-mcp:arm64 --transport http

```


### Amd64

```bash

docker pull ghcr.io/root27/pg-mcp:amd64

```

#### Run Container

```bash

docker run -it  \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=5432 \
  -e DB_USER=postgres \
  -e DB_PASSWORD=secret \
  -e DB_NAME=mydb \
  -e DB_SSLMODE=disable \
  -p 8080:8080 \
  ghcr.io/root27/pg-mcp:amd64 --transport http

```

The server is accessible at http://localhost:8080/mcp




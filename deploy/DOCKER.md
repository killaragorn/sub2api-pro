# Sub2API Docker Image

Sub2API is an AI API Gateway Platform for distributing and managing AI product subscription API quotas.

## Quick Start

```bash
docker run -d \
  --name sub2api \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/sub2api" \
  -e REDIS_URL="redis://host:6379" \
  ghcr.io/killaragorn/sub2api-pro:latest
```

## Docker Compose

```yaml
version: '3.8'

services:
  sub2api:
    image: ghcr.io/killaragorn/sub2api-pro:latest
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://postgres:postgres@db:5432/sub2api?sslmode=disable
      - REDIS_URL=redis://redis:6379
    depends_on:
      - db
      - redis

  db:
    image: postgres:15-alpine
    environment:
      - POSTGRES_USER=postgres
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=sub2api
    volumes:
      - postgres_data:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    volumes:
      - redis_data:/data

volumes:
  postgres_data:
  redis_data:
```

## Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `DATABASE_URL` | PostgreSQL connection string | Yes | - |
| `REDIS_URL` | Redis connection string | Yes | - |
| `PORT` | Server port | No | `8080` |
| `GIN_MODE` | Gin framework mode (`debug`/`release`) | No | `release` |

## Supported Architectures

- `linux/amd64`
- `linux/arm64`

## Tags

- `latest` - Latest stable release
- `x.y.z` - Specific version
- `x.y` - Latest patch of minor version
- `x` - Latest minor of major version

## In-container updates

Release containers can use the admin console's built-in updater. Update checks,
downloads, checksums, and rollback candidates come from
`killaragorn/sub2api-pro` GitHub Releases. The updater atomically replaces
`/app/sub2api`; the container entrypoint keeps that path writable by the
non-root `sub2api` user, and `restart: unless-stopped` starts the updated binary
after the requested restart.

An in-container update survives a normal container or Docker daemon restart,
but recreating the container restores the binary from its image. Pin the
initial release image and avoid recreating the application container unless
you intentionally want to return to that image version.

## Links

- [GitHub Repository](https://github.com/killaragorn/sub2api-pro)
- [Documentation](https://github.com/killaragorn/sub2api-pro#readme)

# Troubleshooting Guide

Common issues and solutions when working with Delos.

## Service Issues

### Port already in use

**Error:** `listen tcp :9001: bind: address already in use`

**Solution:**
```bash
# Find and kill process using the port
lsof -ti:9001 | xargs kill -9

# Or kill all Delos services
make stop-all

# Or find specific process
lsof -i :9001
kill <PID>
```

### Service won't connect to database

**Error:** `failed to connect to postgres` or `connection refused`

**Check:**
1. Is PostgreSQL running?
   ```bash
   docker-compose -f deploy/local/docker-compose.yaml ps postgres
   ```

2. Is the hostname correct?
   - Inside Docker: use `postgres` (service name)
   - Outside Docker: use `localhost`

3. Wait for health check:
   ```bash
   docker-compose -f deploy/local/docker-compose.yaml ps
   # Look for "(healthy)" status
   ```

### Service exits immediately

**Symptoms:** Service starts then stops with no obvious error.

**Check logs:**
```bash
# For Docker services
docker-compose -f deploy/local/docker-compose.yaml logs <service>

# For local services, run in foreground
./bin/runtime  # Instead of make run-all
```

**Common causes:**
- Missing environment variables
- Database not ready
- Port conflict

## Docker Issues

### Containers won't start

**Error:** `Cannot connect to the Docker daemon`

**Solution:**
1. Start Docker Desktop (macOS/Windows)
2. Or start Docker service (Linux):
   ```bash
   sudo systemctl start docker
   ```

### Out of disk space

**Error:** `no space left on device`

**Solution:**
```bash
# Clean up Docker
docker system prune -a

# Remove volumes too (WARNING: deletes data)
docker system prune -a --volumes
```

### Build fails

**Error:** Dockerfile build errors

**Try:**
```bash
# Clean rebuild
docker-compose -f deploy/local/docker-compose.yaml down -v
docker-compose -f deploy/local/docker-compose.yaml build --no-cache
docker-compose -f deploy/local/docker-compose.yaml up -d
```

## Test Issues

### Integration tests fail with "service unavailable"

**Cause:** Services aren't running.

**Solution:**
```bash
# Option 1: Start services first
docker-compose -f deploy/local/docker-compose.yaml up -d
./tests/integration/run.sh

# Option 2: Auto-start services
./tests/integration/run.sh --start-services

# Option 3: Skip if services down (for CI)
./tests/integration/run.sh --skip-if-down
```

### Tests hang

**Cause:** Waiting for dependencies that never become ready.

**Check:**
```bash
# Are containers healthy?
docker-compose -f deploy/local/docker-compose.yaml ps

# Check specific service
docker logs delos-postgres
docker logs delos-redis
```

### CLI tests fail with empty output

**Cause:** CLI binary not built or wrong path.

**Solution:**
```bash
make build-cli
ls -la bin/delos
```

## Build Issues

### `go mod download` fails

**Error:** Module download errors

**Try:**
```bash
go clean -modcache
go mod download
```

### Proto generation fails

**Error:** `buf generate` errors

**Check:**
1. Is buf installed?
   ```bash
   which buf
   # If not: go install github.com/bufbuild/buf/cmd/buf@latest
   ```

2. Are proto files valid?
   ```bash
   buf lint
   ```

### Missing generated code

**Error:** Import errors for `gen/go/...`

**Solution:**
```bash
make proto
```

## CLI Issues

### CLI can't connect to services

**Error:** `connection refused` or timeout

**Check:**
1. Are services running?
   ```bash
   docker-compose -f deploy/local/docker-compose.yaml ps
   ```

2. Are you using correct addresses?
   ```bash
   # Default addresses (localhost)
   ./bin/delos prompt list

   # Custom addresses
   DELOS_PROMPT_ADDR=localhost:9002 ./bin/delos prompt list
   ```

### Command not found

**Error:** `delos: command not found`

**Solution:** Use the full path or add to PATH:
```bash
./bin/delos prompt list

# Or add to PATH
export PATH=$PATH:$(pwd)/bin
delos prompt list
```

## LLM / Runtime Issues

### Completions fail

**Error:** `no providers configured` or API errors

**Check:**
1. Are API keys set?
   ```bash
   # In deploy/local/.env.local
   DELOS_OPENAI_API_KEY=sk-...
   ```

2. Restart runtime service after adding keys:
   ```bash
   docker-compose -f deploy/local/docker-compose.yaml restart runtime
   ```

### Rate limiting

**Error:** `rate limit exceeded`

**Solution:** The runtime service should handle this with retries, but you can:
- Use a different provider
- Add delays between requests
- Check your API quota

## Getting More Help

### Enable debug logging

```bash
# Set log level
export DELOS_LOG_LEVEL=debug

# Or in .env
DELOS_LOG_LEVEL=debug
```

### Check service health

```bash
# Via CLI
./bin/delos observe traces --limit 5

# Via curl (if health endpoint exists)
curl localhost:9002/health
```

### View all logs

```bash
# Docker logs
docker-compose -f deploy/local/docker-compose.yaml logs -f

# Specific service
docker-compose -f deploy/local/docker-compose.yaml logs -f prompt
```

### Reset everything

Nuclear option - clean slate:
```bash
# Stop everything
make down
make stop-all

# Remove Docker volumes (deletes all data)
docker-compose -f deploy/local/docker-compose.yaml down -v

# Clean build artifacts
make clean

# Rebuild and restart
make build
docker-compose -f deploy/local/docker-compose.yaml up -d --build
```

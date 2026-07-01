# Quick Reference: Server Discovery

## For Server Owners (Creating Servers)

```bash
# 1. Set registry URL (one-time setup)
export REGISTRY_URL="http://your-registry-server:5000"
echo 'export REGISTRY_URL="http://your-registry-server:5000"' >> ~/.bashrc

# 2. Submit a server (auto-registers)
./submit_llama.sh --config qwen3-30b

# 3. Check it's running and registered
./list_servers.sh --owner $USER
```

## For Users (Finding & Connecting to Servers)

### Option 1: Web Dashboard (Recommended)
Open browser to: **http://your-registry-server:5000/**
- See all servers
- Copy connection commands
- Auto-refreshes

### Option 2: Command Line
```bash
# List all servers
curl http://your-registry-server:5000/servers | jq

# List servers by specific owner
curl http://your-registry-server:5000/servers/by-owner/username | jq

# Get specific server details
curl http://your-registry-server:5000/servers/JOB_ID | jq
```

### Option 3: If you have shared filesystem access
```bash
# List servers
./list_servers.sh

# Connect directly
./connect_claude_llama.sh JOB_ID
```

## Connecting from Outside the Cluster

1. **Find a server** (web dashboard or API)
2. **Create SSH tunnel**:
   ```bash
   ssh -L LOCAL_PORT:SERVER_HOST:SERVER_PORT your-login-node
   ```
   Example:
   ```bash
   ssh -L 8080:gpu-node-05:52677 user@cluster.example.com
   ```

3. **Connect Claude**:
   ```bash
   export ANTHROPIC_BASE_URL="http://localhost:8080"
   claude
   ```

## Common Tasks

### Check registry health
```bash
curl http://your-registry-server:5000/health
```

### Find who's running what
```bash
curl http://your-registry-server:5000/servers | jq '.servers[] | {owner, model_name, uptime_hours}'
```

### Find servers with specific model
```bash
curl http://your-registry-server:5000/servers | jq '.servers[] | select(.model_name | contains("qwen"))'
```

## Environment Variables

```bash
# Required for auto-registration
export REGISTRY_URL="http://your-registry-server:5000"

# Optional: for list_servers.sh if not using default
export REGISTRY_URL="http://custom-server:8080"
```

## Troubleshooting

**Can't connect to registry?**
```bash
curl http://your-registry-server:5000/health
# Should return: {"status": "ok", "servers": N}
```

**Server not showing up?**
- Check job logs: `tail -f llama_server_JOB_ID.log`
- Verify REGISTRY_URL was set when submitting
- Wait 1-2 minutes for server to fully start

**Old servers still listed?**
- They auto-cleanup after 48 hours
- Or manually delete: `curl -X DELETE http://registry:5000/servers/JOB_ID`

## Security Notes

- Registry has no authentication by default
- Anyone with network access can see/register servers
- Don't expose publicly without adding auth
- Consider firewall rules to restrict access

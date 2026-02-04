# Server Registry Setup

A central registry service that allows users to discover running llama.cpp servers without needing access to the shared filesystem.

## Architecture

- **Registry Server**: Flask-based REST API that tracks active servers
- **Auto-registration**: SLURM jobs automatically register when they start
- **Discovery**: Users query the registry to find available servers
- **Auto-cleanup**: Stale servers are removed after 48 hours of inactivity

## Setup Registry Server

### Option 1: Run as systemd service (Production)

1. Copy files to a shared location:
```bash
sudo mkdir -p /opt/llama-registry
sudo cp registry_server.py /opt/llama-registry/
sudo cp llama-registry.service /etc/systemd/system/
```

2. Install dependencies:
```bash
sudo pip3 install flask
```

3. Edit the service file if needed (change user, port, etc.)

4. Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable llama-registry
sudo systemctl start llama-registry
sudo systemctl status llama-registry
```

### Option 2: Run manually (Testing)

```bash
# Install Flask if needed
pip3 install flask

# Run the server
./registry_server.py --host 0.0.0.0 --port 5000

# Or run in background
nohup ./registry_server.py --host 0.0.0.0 --port 5000 > registry.log 2>&1 &
```

### Option 3: Run in SLURM (if no dedicated server available)

```bash
sbatch --job-name=llama-registry \
       --time=7-00:00:00 \
       --mem=2G \
       --cpus-per-task=2 \
       --wrap="./registry_server.py --host 0.0.0.0 --port 5000"
```

## Configure Clients

Set the registry URL in your environment:

```bash
# In your ~/.bashrc or job submission script
export REGISTRY_URL="http://your-server:5000"
```

Or create a config file:
```bash
echo 'export REGISTRY_URL="http://your-server:5000"' > ~/.llama_registry_config
source ~/.llama_registry_config
```

## Usage

### Submit a server (auto-registers if REGISTRY_URL is set)

```bash
export REGISTRY_URL="http://your-server:5000"
./submit_llama.sh --config qwen3-30b
```

### View the web dashboard

Open your browser to:
```
http://your-server:5000/
```

The dashboard shows:
- All active servers with their details
- Connection commands you can copy
- Real-time updates (auto-refreshes every 30s)
- Filtering by owner

### List available servers (CLI)

```bash
# List all servers
./list_servers.sh

# List your servers only
./list_servers.sh --owner $USER

# Get raw JSON
./list_servers.sh --json
```

### Connect to a server

```bash
# Same as before - works with or without registry
./connect_claude.sh <job_id>
```

## API Endpoints

### List all servers
```bash
curl http://your-server:5000/servers
```

### Get specific server
```bash
curl http://your-server:5000/servers/<job_id>
```

### Register a server (manual)
```bash
curl -X POST http://your-server:5000/servers/<job_id> \
  -H "Content-Type: application/json" \
  -d '{
    "host": "gpu-node-01",
    "port": "52677",
    "owner": "username",
    "model_name": "qwen3-30b",
    "model_file": "/path/to/model.gguf"
  }'
```

### Delete a server
```bash
curl -X DELETE http://your-server:5000/servers/<job_id>
```

### List servers by owner
```bash
curl http://your-server:5000/servers/by-owner/<username>
```

## For Users Outside Shared Filesystem

If you don't have access to the shared filesystem where the scripts are:

### Option 1: Use the Web Dashboard (Easiest)

Open your browser to:
```
http://your-registry-server:5000/
```

You'll see all available servers with connection details you can copy.

### Option 2: Use the API directly

1. Set the registry URL:
```bash
export REGISTRY_URL="http://your-server:5000"
```

2. List available servers:
```bash
curl http://your-server:5000/servers | jq
```

3. Get connection info for a server:
```bash
curl http://your-server:5000/servers/<job_id> | jq -r '"Host: \(.host)\nPort: \(.port)\nModel: \(.model_name)"'
```

4. Create SSH tunnel to access the server:
```bash
# From your local machine
ssh -L 8080:<host>:<port> your-login-node

# Then set up Claude to use localhost:8080
export ANTHROPIC_BASE_URL="http://localhost:8080"
claude
```

## Security Considerations

- **No authentication**: Currently anyone can register/query servers
- **Network access**: Registry must be accessible from compute nodes and users
- **Port conflicts**: Each server uses a random port (managed automatically)

### Optional: Add basic authentication

Edit `registry_server.py` to add API key authentication:

```python
from flask import request, abort

API_KEY = "your-secret-key"

@app.before_request
def check_auth():
    if request.endpoint != 'health':
        key = request.headers.get('X-API-Key')
        if key != API_KEY:
            abort(401)
```

Then use with:
```bash
curl -H "X-API-Key: your-secret-key" http://your-server:5000/servers
```

## Monitoring

Check registry health:
```bash
curl http://your-server:5000/health
```

View registry logs:
```bash
# If running as systemd service
sudo journalctl -u llama-registry -f

# If running manually
tail -f registry.log
```

## Backup and Persistence

The registry stores data in `~/.cache/llama_server_registry.json` (or `/opt/llama-registry/` if running as service).

To backup:
```bash
cp ~/.cache/llama_server_registry.json ~/backup/
```

To restore:
```bash
cp ~/backup/llama_server_registry.json ~/.cache/
# Restart the service
sudo systemctl restart llama-registry
```

## Integration with Web UI

A web dashboard is included at `dashboard.html` and automatically served at the root URL (`/`) of the registry server.

Features:
- Real-time server list with auto-refresh
- Filter by owner
- Copy connection commands with one click
- Shows uptime, model info, and connection details
- Mobile-friendly responsive design

To customize the dashboard:
1. Edit `dashboard.html`
2. Restart the registry server (changes are loaded on startup)

You can also host it separately on any web server - just update the registry URL in the dashboard's input field.

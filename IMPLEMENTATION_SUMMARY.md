# Server Registry Implementation Summary

## What Was Built

A complete central registry system for discovering llama.cpp servers without requiring shared filesystem access.

## Components Created

### 1. Registry Server (`registry_server.py`)
- Flask REST API for server registration and discovery
- In-memory storage with disk persistence
- Auto-cleanup of stale servers (48h)
- Serves web dashboard at root URL
- Endpoints:
  - `GET /health` - Health check
  - `GET /servers` - List all servers
  - `GET /servers/<job_id>` - Get specific server
  - `POST /servers/<job_id>` - Register/update server
  - `DELETE /servers/<job_id>` - Unregister server
  - `GET /servers/by-owner/<owner>` - Filter by owner

### 2. Auto-Registration (`register_server.sh`)
- Called by SLURM jobs after server startup
- Registers server with retry logic
- Sends metadata: host, port, owner, model, start time

### 3. Server Discovery CLI (`list_servers.sh`)
- Command-line tool to list available servers
- Supports filtering by owner
- JSON or pretty-printed output

### 4. Web Dashboard (`dashboard.html`)
- Modern, responsive UI
- Real-time server list with auto-refresh
- One-click copy of connection commands
- Filter by owner
- Shows uptime, model info, connection details

### 5. Integration with Existing Scripts
- Modified `llama_server.slurm` to auto-register/unregister
- Modified `llama_server.slurm` to send email notifications
- Updated `submit_llama.sh` to accept `--email` parameter
- Updated `README.md` with registry and notification documentation
- Created `REGISTRY_SETUP.md` for setup instructions
- Created `QUICKSTART_REGISTRY.md` for quick reference
- Created `EMAIL_NOTIFICATIONS.md` for notification setup

### 6. Email Notifications (`send_notification.sh`)
- Sends email when server is ready
- Includes connection details and instructions
- Supports mail, sendmail, and mutt
- Optional email parameter in submit script
- Can use NOTIFY_EMAIL environment variable

### 7. Push Notifications (`send_ntfy_notification.sh`)
- Sends push notifications via ntfy.sh
- Instant mobile and desktop notifications
- No signup or auth required (public server)
- Supports custom ntfy servers
- Includes clickable dashboard link
- Can use NTFY_TOPIC and NTFY_SERVER environment variables

### 6. Testing (`test_registry.sh`)
- Automated test suite for all endpoints
- Verifies registration, listing, deletion

### 8. Deployment Files
- `llama-registry.service` - systemd service file
- Documentation for multiple deployment options

### 9. Optional Email Helper (`send_registry_email.py`)
- Python script for sending emails via registry
- Can be integrated with registry server for centralized notifications

## How It Works

### Server Submission Flow
1. User sets `REGISTRY_URL` environment variable (optional)
2. User sets `NOTIFY_EMAIL` or passes `--email` (optional)
3. User sets `NTFY_TOPIC` or passes `--ntfy-topic` (optional)
4. User submits job: `./submit_llama.sh --config qwen3-30b --email user@example.com --ntfy-topic my-servers`
5. SLURM job starts llama-server
6. Script waits for server health check
7. `register_server.sh` sends POST to registry with server details (if REGISTRY_URL set)
8. `send_notification.sh` sends email to user (if email provided)
9. `send_ntfy_notification.sh` sends push notification (if ntfy topic provided)
10. Server info is stored in registry
11. On job termination, sends DELETE to unregister

### Discovery Flow
1. User opens web dashboard or runs `./list_servers.sh`
2. Registry returns list of active servers
3. User gets connection details (host, port, job_id)
4. User creates SSH tunnel if outside cluster
5. User connects Claude to tunneled endpoint

## Benefits

### For Users WITH Shared Filesystem
- Same workflow as before
- Bonus: can see other users' servers
- Web dashboard for easy discovery

### For Users WITHOUT Shared Filesystem
- Can discover servers via web dashboard
- Can query API directly
- No need for connection files
- Just need: registry URL + SSH access

## Deployment Options

1. **Systemd service** (production) - Always running on dedicated node
2. **Manual process** (development) - Run on demand
3. **SLURM job** (no dedicated server) - Run registry as long-running job

## Security Considerations

- No authentication by default
- Suitable for trusted internal networks
- Can add API key auth if needed
- Should not be exposed to public internet without security

## Future Enhancements (Not Implemented)

- Authentication/API keys
- WebSocket for real-time updates
- Prometheus metrics endpoint
- Load balancing/routing
- Resource usage tracking
- Reservation system
- Slack/email notifications

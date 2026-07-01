# Push Notifications with ntfy

Get instant push notifications on your phone or desktop when llama.cpp servers are ready using [ntfy.sh](https://ntfy.sh).

## What is ntfy?

ntfy is a simple HTTP-based pub-sub notification service. You can send notifications from anywhere, and receive them on your phone, desktop, or via webhooks - no signup required!

## Quick Start

### 1. Subscribe to Notifications

**On your phone:**
1. Install ntfy app: [iOS](https://apps.apple.com/us/app/ntfy/id1625396347) | [Android](https://play.google.com/store/apps/details?id=io.heckel.ntfy)
2. Open the app
3. Tap "+" to add a new topic
4. Enter your topic name (e.g., "my-llama-servers")
5. Tap "Subscribe"

**On your desktop:**
```bash
# Via web browser
open https://ntfy.sh/my-llama-servers

# Or subscribe via CLI
ntfy subscribe my-llama-servers
```

### 2. Submit Jobs with ntfy

```bash
# Basic usage
./submit_llama.sh --config qwen3-30b --ntfy-topic my-llama-servers

# With custom server
./submit_llama.sh --config qwen3-30b \
  --ntfy-topic servers \
  --ntfy-server https://ntfy.mycompany.com
```

### 3. Receive Notification

When your server is ready, you'll get a push notification with:
- ✅ Server details (job ID, model, host, port)
- 🔗 Clickable link to web dashboard (if registry enabled)
- 📋 Quick connect command
- ⏰ Start time

## Configuration

### Environment Variables

Set default values in your `~/.bashrc`:

```bash
# Use public ntfy.sh
export NTFY_TOPIC="my-llama-servers"

# Or use your own server
export NTFY_TOPIC="servers"
export NTFY_SERVER="https://ntfy.mycompany.com"
```

Then submit jobs without flags:
```bash
./submit_llama.sh --config qwen3-30b
```

### Per-Job Configuration

Override defaults for specific jobs:
```bash
./submit_llama.sh --config qwen3-30b --ntfy-topic urgent-jobs
```

## Example Notification

```
🦙 Llama Server Ready - Job 2884607

Model: GLM-4.7-Flash-UD-Q4_K_XL.gguf
Host: gpu-node-05:52677
Started: 2026-01-29 17:30:45

Quick connect:
./connect_claude_llama.sh 2884607

Dashboard: https://registry-server:5000/

[Open Dashboard Button]
```

## Advanced Usage

### Multiple Topics

Subscribe to multiple topics for different purposes:

```bash
# Personal servers
./submit_llama.sh --config qwen3-30b --ntfy-topic my-servers

# Team servers
./submit_llama.sh --config qwen3-80b --ntfy-topic team-servers

# Production servers
./submit_llama.sh --config glm-4.7 --ntfy-topic prod-alerts
```

### Priority Levels

The script sends notifications with "high" priority by default. To customize, edit `send_ntfy_notification.sh`:

```bash
# In send_ntfy_notification.sh, change:
-H "Priority: high"

# To:
-H "Priority: urgent"   # or: max, high, default, low, min
```

### Custom Icons and Tags

Edit `send_ntfy_notification.sh` to customize appearance:

```bash
# Change tags (emoji shortcuts)
-H "Tags: white_check_mark,llama"

# To:
-H "Tags: rocket,computer,zap"  # 🚀💻⚡

# Add custom icon
-H "Icon: https://your-server.com/llama-icon.png"
```

### Authentication

For private ntfy servers with authentication:

```bash
# In send_ntfy_notification.sh, add:
-u "username:password"

# Or use tokens:
-H "Authorization: Bearer YOUR_TOKEN"
```

## Self-Hosting ntfy

For privacy or reliability, host your own ntfy server:

### Docker

```bash
docker run -p 80:80 -v /var/cache/ntfy:/var/cache/ntfy binwiederhier/ntfy serve
```

### systemd

```bash
# Install
wget https://github.com/binwiederhier/ntfy/releases/download/v2.8.0/ntfy_2.8.0_linux_amd64.tar.gz
tar xzf ntfy_2.8.0_linux_amd64.tar.gz
sudo cp ntfy /usr/local/bin/
sudo ntfy serve --listen-http :8080
```

Then configure clients:
```bash
export NTFY_SERVER="https://your-ntfy-server.com"
```

See [ntfy docs](https://docs.ntfy.sh) for full setup guide.

## Integration with Other Services

### Slack

Forward ntfy notifications to Slack:

```bash
# Subscribe to ntfy and forward to Slack webhook
ntfy subscribe my-llama-servers | while read msg; do
  curl -X POST $SLACK_WEBHOOK -d "{\"text\": \"$msg\"}"
done
```

### Discord

```bash
ntfy subscribe my-llama-servers | while read msg; do
  curl -X POST $DISCORD_WEBHOOK -d "{\"content\": \"$msg\"}"
done
```

### Home Assistant

```yaml
# configuration.yaml
notify:
  - platform: ntfy
    url: https://ntfy.sh
    topic: my-llama-servers
```

## Privacy & Security

### Public ntfy.sh

**Pros:**
- No setup required
- Free
- Reliable

**Cons:**
- Anyone who knows your topic name can subscribe
- Messages are not encrypted in transit to ntfy.sh (though HTTPS is used)
- No guaranteed uptime

**Best practices:**
- Use unique, hard-to-guess topic names (e.g., "llama-servers-a8f3d92c")
- Don't include sensitive data in notifications
- Consider self-hosting for production use

### Self-Hosted

**Pros:**
- Full control over data
- Can add authentication
- Can enable message encryption
- Can configure access control

**Cons:**
- Requires setup and maintenance
- Need to manage SSL certificates
- Need to ensure availability

## Troubleshooting

### Notifications not received

1. **Check topic name:**
   ```bash
   echo $NTFY_TOPIC
   # Should match what you subscribed to
   ```

2. **Test manually:**
   ```bash
   curl -d "Test message" https://ntfy.sh/your-topic-name
   ```

3. **Check job logs:**
   ```bash
   tail -f llama_server_<job_id>.log
   # Look for "Push notification sent successfully"
   ```

4. **Verify ntfy server is accessible:**
   ```bash
   curl https://ntfy.sh/health
   # Should return: {"healthy":true}
   ```

### Can't connect to custom ntfy server

1. **Check URL:**
   ```bash
   curl $NTFY_SERVER/health
   ```

2. **Check firewall:**
   - Ensure compute nodes can reach ntfy server
   - Check if proxy is required

3. **Check authentication:**
   - If server requires auth, add to `send_ntfy_notification.sh`

### Notification content incorrect

1. **Check environment variables:**
   ```bash
   env | grep NTFY
   env | grep REGISTRY
   ```

2. **Test notification script:**
   ```bash
   ./send_ntfy_notification.sh 12345 gpu-01 8080 /path/model.gguf my-topic
   ```

## Comparison: Email vs ntfy

| Feature | Email | ntfy |
|---------|-------|------|
| Setup | Requires SMTP | No setup needed |
| Speed | Slower (SMTP delays) | Instant |
| Mobile | Native email app | Dedicated ntfy app |
| Desktop | Any email client | Web or CLI |
| Reliability | Depends on SMTP | Very reliable |
| Privacy | More private | Public topics visible |
| Formatting | HTML support | Plain text + markdown |
| Attachments | Yes | No |
| Cost | Usually free | Free (public) |

**Recommendation:** Use both!
```bash
./submit_llama.sh --config qwen3-30b \
  --email user@example.com \
  --ntfy-topic my-servers
```

Email for detailed records, ntfy for instant alerts.

## API Reference

The `send_ntfy_notification.sh` script uses the ntfy HTTP API:

```bash
curl -X POST "https://ntfy.sh/my-topic" \
  -H "Title: Server Ready" \
  -H "Priority: high" \
  -H "Tags: white_check_mark,llama" \
  -H "Actions: view, Open Dashboard, https://dashboard.com/" \
  -d "Server is ready at gpu-01:8080"
```

**Headers:**
- `Title` - Notification title
- `Priority` - Urgency level (min, low, default, high, urgent, max)
- `Tags` - Emoji shortcuts (comma-separated)
- `Actions` - Clickable buttons
- `Icon` - Custom icon URL
- `Attach` - File URL to attach

See [ntfy documentation](https://docs.ntfy.sh/publish/) for all options.

## Examples

### Basic notification
```bash
./submit_llama.sh --config qwen3-30b --ntfy-topic servers
```

### With email backup
```bash
./submit_llama.sh --config qwen3-30b \
  --ntfy-topic servers \
  --email user@example.com
```

### Private server
```bash
./submit_llama.sh --config qwen3-30b \
  --ntfy-topic team-servers \
  --ntfy-server https://ntfy.internal.company.com
```

### All notifications enabled
```bash
export REGISTRY_URL="http://registry:5000"
export NOTIFY_EMAIL="user@example.com"
export NTFY_TOPIC="my-servers"

./submit_llama.sh --config qwen3-30b

# You'll get:
# 1. Push notification on phone
# 2. Email with full details
# 3. Server in web dashboard
```

## Resources

- **ntfy website:** https://ntfy.sh
- **Documentation:** https://docs.ntfy.sh
- **GitHub:** https://github.com/binwiederhier/ntfy
- **iOS app:** https://apps.apple.com/us/app/ntfy/id1625396347
- **Android app:** https://play.google.com/store/apps/details?id=io.heckel.ntfy
- **Self-hosting guide:** https://docs.ntfy.sh/install/

## Related Documentation

- [README.md](README.md) - Main documentation
- [EMAIL_NOTIFICATIONS.md](EMAIL_NOTIFICATIONS.md) - Email setup
- [REGISTRY_SETUP.md](REGISTRY_SETUP.md) - Registry setup

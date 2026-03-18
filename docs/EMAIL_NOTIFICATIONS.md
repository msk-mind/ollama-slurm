# Email Notification Setup

Get email notifications when your llama.cpp servers are ready to use.

## Quick Start

```bash
# Submit with email notification
./submit_llama.sh --config qwen3-30b --email your.email@example.com

# Or set default email in environment
export NOTIFY_EMAIL="your.email@example.com"
./submit_llama.sh --config qwen3-30b
```

## What You'll Receive

When your server is ready, you'll get an email with:

1. **Server Details**
   - Job ID
   - Model name
   - Host and port
   - Start time

2. **Connection Instructions**
   - Quick connect command
   - Manual connection steps
   - SSH tunnel setup (for remote access)
   - Web dashboard link (if registry enabled)

3. **Management Commands**
   - Check job status
   - View logs
   - Cancel job

## Example Email

```
Subject: Llama Server Ready - Job 2884607

Your llama.cpp server is now running and ready to use!

========================================
Server Details
========================================
Job ID:      2884607
Model:       GLM-4.7-Flash-UD-Q4_K_XL.gguf
Host:        gpu-node-05
Port:        52677
Started:     2026-01-29 16:30:45

========================================
How to Connect
========================================

Option 1: Quick Connect (if on shared filesystem)
--------------------------------------------------
cd /gpfs/mskmind_ess/limr/repos/ollama-slurm
./connect_claude_llama.sh 2884607

Option 2: Manual Connection
--------------------------------------------------
source llama_server_connection_2884607.txt
source setup_claude_env.sh
claude

Option 3: From Outside Cluster (SSH Tunnel)
--------------------------------------------------
# Create tunnel from your local machine:
ssh -L 52677:gpu-node-05:52677 user@cluster.example.com

# Then in another terminal:
export ANTHROPIC_BASE_URL="http://localhost:52677"
claude

Option 4: Web Dashboard
--------------------------------------------------
View all servers at: http://registry-server:5000/
```

## System Requirements

The email notification script supports multiple email clients and will use the first available:

1. **mail** (mailx) - Most common on Linux systems
2. **sendmail** - Standard SMTP client
3. **mutt** - Alternative email client

### Check if email is configured

```bash
# Test with mail
echo "Test message" | mail -s "Test Subject" your.email@example.com

# Test with sendmail
echo -e "To: your.email@example.com\nSubject: Test\n\nTest message" | sendmail -t

# Check which email client is available
which mail sendmail mutt
```

## Configuration

### Set Default Email Address

Add to your `~/.bashrc`:

```bash
export NOTIFY_EMAIL="your.email@example.com"
```

### Disable Email Notifications

```bash
# Don't pass --email flag and don't set NOTIFY_EMAIL
./submit_llama.sh --config qwen3-30b
```

### Custom Email Format

Edit `send_notification.sh` to customize the email content, subject, or formatting.

## Troubleshooting

### Email not received

1. **Check if email client is installed:**
   ```bash
   which mail sendmail mutt
   ```

2. **Check job logs:**
   ```bash
   tail -f llama_server_<job_id>.log
   # Look for "Email notification sent to: ..."
   ```

3. **Test email manually:**
   ```bash
   ./send_notification.sh <job_id> <host> <port> <model_file> your.email@example.com
   ```

4. **Check spam folder**

### Email sent but content is garbled

- Some email clients may require different encoding
- Edit `send_notification.sh` and modify the email format

### SMTP not configured on compute nodes

If compute nodes can't send email directly:

**Option 1:** Use login node relay
```bash
# In send_notification.sh, use SSH to login node:
ssh login-node "mail -s '$EMAIL_SUBJECT' '$EMAIL' <<< '$EMAIL_BODY'"
```

**Option 2:** Use registry server notifications
- The registry server can send emails on behalf of servers
- Requires additional setup in `registry_server.py`

**Option 3:** Use external notification service
- Integrate with Slack, Teams, or other messaging platforms
- See `REGISTRY_SETUP.md` for webhook options

## Integration with Registry

When using the central registry, email address is stored with the server registration. Future enhancements could include:

- Email all users when a specific model becomes available
- Digest emails of available servers
- Notifications when servers go offline
- Reservation notifications

## Privacy & Security

- Email addresses are stored temporarily in SLURM environment variables
- If using registry, emails are stored in the registry database
- Consider using distribution lists or aliases instead of personal emails
- Email content includes connection details - ensure your email is secure

## Advanced: Custom Notifications

### Slack Webhook

Create a custom notification script using Slack webhooks:

```bash
#!/bin/bash
SLACK_WEBHOOK="https://hooks.slack.com/services/YOUR/WEBHOOK/URL"

curl -X POST $SLACK_WEBHOOK -H 'Content-Type: application/json' -d '{
  "text": "🦙 Llama Server Ready!",
  "attachments": [{
    "color": "good",
    "fields": [
      {"title": "Job ID", "value": "'$JOB_ID'", "short": true},
      {"title": "Model", "value": "'$MODEL_NAME'", "short": true},
      {"title": "Host", "value": "'$HOST:$PORT'", "short": true}
    ]
  }]
}'
```

### Microsoft Teams

```bash
TEAMS_WEBHOOK="https://outlook.office.com/webhook/YOUR/WEBHOOK/URL"

curl -X POST $TEAMS_WEBHOOK -H 'Content-Type: application/json' -d '{
  "@type": "MessageCard",
  "title": "Llama Server Ready",
  "text": "Job '$JOB_ID' is ready on '$HOST:$PORT'"
}'
```

### SMS via Twilio

```bash
# Requires Twilio account and credentials
curl -X POST "https://api.twilio.com/2010-04-01/Accounts/$TWILIO_ACCOUNT_SID/Messages.json" \
  --data-urlencode "Body=Llama server $JOB_ID ready on $HOST:$PORT" \
  --data-urlencode "From=$TWILIO_PHONE" \
  --data-urlencode "To=$YOUR_PHONE" \
  -u $TWILIO_ACCOUNT_SID:$TWILIO_AUTH_TOKEN
```

## Related Documentation

- [REGISTRY_SETUP.md](REGISTRY_SETUP.md) - Central registry setup
- [QUICKSTART_REGISTRY.md](QUICKSTART_REGISTRY.md) - Quick reference
- [README.md](README.md) - Main documentation

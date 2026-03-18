#!/usr/bin/env python3
"""
Helper script to send email notifications via registry API
Can be used by registry server to notify users when servers are registered
"""

import sys
import json
import smtplib
from email.mime.text import MIMEText
from email.mime.multipart import MIMEMultipart

def send_email(to_email, job_id, host, port, model_name, owner, registry_url):
    """Send email notification about new server"""
    
    subject = f"Llama Server Ready - Job {job_id}"
    
    body = f"""Your llama.cpp server is now running and ready to use!

========================================
Server Details
========================================
Job ID:      {job_id}
Owner:       {owner}
Model:       {model_name}
Host:        {host}
Port:        {port}

========================================
How to Connect
========================================

Option 1: Web Dashboard
--------------------------------------------------
View server details at: {registry_url}/

Option 2: SSH Tunnel
--------------------------------------------------
# Create tunnel from your local machine:
ssh -L {port}:{host}:{port} <your-username>@<login-node>

# Then in another terminal:
export ANTHROPIC_BASE_URL="http://localhost:{port}"
claude

Option 3: Direct API Query
--------------------------------------------------
curl {registry_url}/servers/{job_id}

========================================
This is an automated notification from the llama.cpp registry service.
"""

    msg = MIMEMultipart()
    msg['From'] = f"llama-registry@{host}"
    msg['To'] = to_email
    msg['Subject'] = subject
    msg.attach(MIMEText(body, 'plain'))
    
    try:
        # Use local sendmail
        server = smtplib.SMTP('localhost')
        server.send_message(msg)
        server.quit()
        return True
    except Exception as e:
        print(f"Failed to send email: {e}", file=sys.stderr)
        return False

if __name__ == '__main__':
    if len(sys.argv) < 7:
        print("Usage: send_registry_email.py <email> <job_id> <host> <port> <model_name> <owner> <registry_url>")
        sys.exit(1)
    
    success = send_email(
        sys.argv[1],  # to_email
        sys.argv[2],  # job_id
        sys.argv[3],  # host
        sys.argv[4],  # port
        sys.argv[5],  # model_name
        sys.argv[6],  # owner
        sys.argv[7]   # registry_url
    )
    
    sys.exit(0 if success else 1)

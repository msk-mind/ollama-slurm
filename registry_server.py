#!/usr/bin/env python3
"""
Central registry service for llama.cpp servers.
Allows users to register and discover running servers without shared filesystem access.
"""

from flask import Flask, request, jsonify
from datetime import datetime, timedelta
import json
import os
import threading
import time

app = Flask(__name__)

# In-memory storage (could be replaced with Redis, SQLite, etc.)
servers = {}
lock = threading.Lock()

REGISTRY_FILE = os.path.expanduser("~/.cache/llama_server_registry.json")
CLEANUP_INTERVAL = 300  # 5 minutes
MAX_AGE_HOURS = 48

def load_registry():
    """Load registry from disk on startup."""
    global servers
    os.makedirs(os.path.dirname(REGISTRY_FILE), exist_ok=True)
    if os.path.exists(REGISTRY_FILE):
        try:
            with open(REGISTRY_FILE, 'r') as f:
                servers = json.load(f)
                print(f"Loaded {len(servers)} servers from registry")
        except Exception as e:
            print(f"Error loading registry: {e}")

def save_registry():
    """Save registry to disk."""
    try:
        with open(REGISTRY_FILE, 'w') as f:
            json.dump(servers, f, indent=2)
    except Exception as e:
        print(f"Error saving registry: {e}")

def cleanup_old_servers():
    """Remove servers that haven't been updated recently."""
    while True:
        time.sleep(CLEANUP_INTERVAL)
        with lock:
            cutoff = (datetime.now() - timedelta(hours=MAX_AGE_HOURS)).isoformat()
            old_keys = [k for k, v in servers.items() if v.get('last_updated', '') < cutoff]
            for key in old_keys:
                print(f"Removing stale server: {key}")
                del servers[key]
            if old_keys:
                save_registry()

@app.route('/health', methods=['GET'])
def health():
    """Health check endpoint."""
    return jsonify({"status": "ok", "servers": len(servers)})

@app.route('/servers', methods=['GET'])
def list_servers():
    """List all active servers."""
    with lock:
        # Add computed fields
        server_list = []
        for job_id, info in servers.items():
            server_info = info.copy()
            server_info['job_id'] = job_id
            # Calculate uptime
            if 'start_time' in info:
                start = datetime.fromisoformat(info['start_time'])
                uptime_seconds = (datetime.now() - start).total_seconds()
                server_info['uptime_hours'] = round(uptime_seconds / 3600, 1)
            server_list.append(server_info)
        
        # Sort by start time (newest first)
        server_list.sort(key=lambda x: x.get('start_time', ''), reverse=True)
        
        return jsonify({
            "servers": server_list,
            "count": len(server_list)
        })

@app.route('/servers/<job_id>', methods=['GET'])
def get_server(job_id):
    """Get specific server info."""
    with lock:
        if job_id not in servers:
            return jsonify({"error": "Server not found"}), 404
        return jsonify(servers[job_id])

@app.route('/servers/<job_id>', methods=['POST', 'PUT'])
def register_server(job_id):
    """Register or update a server."""
    data = request.get_json()
    
    required = ['host', 'port']
    if not all(k in data for k in required):
        return jsonify({"error": f"Missing required fields: {required}"}), 400
    
    with lock:
        # Add metadata
        data['last_updated'] = datetime.now().isoformat()
        if job_id not in servers:
            data['start_time'] = data.get('start_time', datetime.now().isoformat())
        else:
            # Preserve start_time on updates
            data['start_time'] = servers[job_id].get('start_time', datetime.now().isoformat())
        
        servers[job_id] = data
        save_registry()
    
    return jsonify({"status": "registered", "job_id": job_id})

@app.route('/servers/<job_id>', methods=['DELETE'])
def unregister_server(job_id):
    """Remove a server from registry."""
    with lock:
        if job_id in servers:
            del servers[job_id]
            save_registry()
            return jsonify({"status": "deleted", "job_id": job_id})
        return jsonify({"error": "Server not found"}), 404

@app.route('/servers/by-owner/<owner>', methods=['GET'])
def list_by_owner(owner):
    """List servers by owner."""
    with lock:
        owner_servers = {k: v for k, v in servers.items() if v.get('owner') == owner}
        return jsonify({
            "servers": owner_servers,
            "count": len(owner_servers)
        })

@app.route('/')
def dashboard():
    """Serve the web dashboard."""
    dashboard_file = os.path.join(os.path.dirname(__file__), 'dashboard.html')
    if os.path.exists(dashboard_file):
        with open(dashboard_file, 'r') as f:
            return f.read()
    return """
    <html>
    <body>
        <h1>Llama Server Registry</h1>
        <p>API Endpoints:</p>
        <ul>
            <li><a href="/health">/health</a> - Health check</li>
            <li><a href="/servers">/servers</a> - List all servers</li>
        </ul>
    </body>
    </html>
    """

if __name__ == '__main__':
    import argparse
    
    parser = argparse.ArgumentParser(description='Llama server registry service')
    parser.add_argument('--host', default='0.0.0.0', help='Host to bind to')
    parser.add_argument('--port', type=int, default=5000, help='Port to bind to')
    parser.add_argument('--debug', action='store_true', help='Enable debug mode')
    args = parser.parse_args()
    
    # Load existing registry
    load_registry()
    
    # Start cleanup thread
    cleanup_thread = threading.Thread(target=cleanup_old_servers, daemon=True)
    cleanup_thread.start()
    
    print(f"Starting registry server on {args.host}:{args.port}")
    print(f"Registry file: {REGISTRY_FILE}")
    
    app.run(host=args.host, port=args.port, debug=args.debug)

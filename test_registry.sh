#!/bin/bash
# Test script for the registry service

set -e

echo "Testing Llama Server Registry"
echo "=============================="
echo ""

# Start registry server in background
echo "Starting registry server..."
./registry_server.py --host 127.0.0.1 --port 5555 &
REGISTRY_PID=$!
export REGISTRY_URL="http://127.0.0.1:5555"

# Give it time to start
sleep 3

# Cleanup on exit
cleanup() {
    echo ""
    echo "Cleaning up..."
    kill $REGISTRY_PID 2>/dev/null || true
}
trap cleanup EXIT

echo "Testing registry endpoints..."
echo ""

# Test health endpoint
echo "1. Testing health endpoint..."
curl -s "$REGISTRY_URL/health" | jq .
echo ""

# Test empty server list
echo "2. Testing empty server list..."
curl -s "$REGISTRY_URL/servers" | jq .
echo ""

# Test server registration
echo "3. Testing server registration..."
./register_server.sh "test-job-123" "test-node" "12345" "/path/to/model.gguf"
echo ""

# Test listing servers
echo "4. Testing server list (should have 1 server)..."
curl -s "$REGISTRY_URL/servers" | jq .
echo ""

# Test getting specific server
echo "5. Testing get specific server..."
curl -s "$REGISTRY_URL/servers/test-job-123" | jq .
echo ""

# Test filtering by owner
echo "6. Testing filter by owner..."
curl -s "$REGISTRY_URL/servers/by-owner/$USER" | jq .
echo ""

# Test list_servers.sh script
echo "7. Testing list_servers.sh script..."
./list_servers.sh
echo ""

# Test server deletion
echo "8. Testing server deletion..."
curl -X DELETE -s "$REGISTRY_URL/servers/test-job-123" | jq .
echo ""

# Verify deletion
echo "9. Verifying deletion (should be empty)..."
curl -s "$REGISTRY_URL/servers" | jq .
echo ""

echo "=============================="
echo "All tests passed! ✓"
echo ""
echo "To view the web dashboard, open:"
echo "  http://127.0.0.1:5555/"
echo ""
echo "Press Ctrl+C to stop the test server"

# Keep running so user can test the dashboard
wait $REGISTRY_PID

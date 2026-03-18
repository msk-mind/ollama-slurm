"""
Tests for registry_server.py using Flask's built-in test client.
No running server required — tests are fast and self-contained.

Run with:
    # from the repo root:
    .venv/bin/python3 -m pytest tests/test_registry.py -v

    # single test:
    .venv/bin/python3 -m pytest tests/test_registry.py::TestRegistration::test_update_preserves_start_time -v
"""

import json
import os
import sys
import tempfile
import threading
import time
import unittest
from datetime import datetime, timedelta
from unittest.mock import patch

# Allow importing registry_server from the registry/ subdirectory
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "registry"))

# ---------------------------------------------------------------------------
# Import the Flask app.  We reset global state before each test so tests
# are fully isolated from one another.
# ---------------------------------------------------------------------------
import registry_server as rs


def _make_client(registry_file: str):
    """Return a fresh Flask test client backed by an empty registry."""
    rs.servers = {}
    rs.REGISTRY_FILE = registry_file
    rs.app.config["TESTING"] = True
    return rs.app.test_client()


class RegistryTestBase(unittest.TestCase):
    """Base class: creates a temp registry file and test client per test."""

    def setUp(self):
        self._tmp = tempfile.NamedTemporaryFile(suffix=".json", delete=False)
        self._tmp.close()
        self.registry_file = self._tmp.name
        self.client = _make_client(self.registry_file)

    def tearDown(self):
        rs.servers = {}
        os.unlink(self.registry_file)

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------
    def _register(self, job_id="job-1", host="node-01", port="12345",
                   owner="alice", model="my-model.gguf", extra=None):
        payload = {"host": host, "port": port, "owner": owner,
                   "model_name": model}
        if extra:
            payload.update(extra)
        return self.client.post(
            f"/servers/{job_id}",
            data=json.dumps(payload),
            content_type="application/json",
        )

    def _json(self, response):
        return json.loads(response.data)


# ===========================================================================
# Health endpoint
# ===========================================================================
class TestHealth(RegistryTestBase):

    def test_health_returns_ok(self):
        r = self.client.get("/health")
        self.assertEqual(r.status_code, 200)
        body = self._json(r)
        self.assertEqual(body["status"], "ok")

    def test_health_reflects_server_count(self):
        self._register(job_id="j1")
        self._register(job_id="j2")
        body = self._json(self.client.get("/health"))
        self.assertEqual(body["servers"], 2)


# ===========================================================================
# Registration  (POST / PUT)
# ===========================================================================
class TestRegistration(RegistryTestBase):

    def test_register_returns_registered(self):
        r = self._register(job_id="job-42")
        self.assertEqual(r.status_code, 200)
        body = self._json(r)
        self.assertEqual(body["status"], "registered")
        self.assertEqual(body["job_id"], "job-42")

    def test_register_missing_host_returns_400(self):
        r = self.client.post(
            "/servers/bad-job",
            data=json.dumps({"port": "9999"}),
            content_type="application/json",
        )
        self.assertEqual(r.status_code, 400)
        self.assertIn("error", self._json(r))

    def test_register_missing_port_returns_400(self):
        r = self.client.post(
            "/servers/bad-job",
            data=json.dumps({"host": "node-01"}),
            content_type="application/json",
        )
        self.assertEqual(r.status_code, 400)

    def test_put_also_registers(self):
        r = self.client.put(
            "/servers/job-put",
            data=json.dumps({"host": "n1", "port": "1111"}),
            content_type="application/json",
        )
        self.assertEqual(r.status_code, 200)
        self.assertEqual(self._json(r)["status"], "registered")

    def test_update_preserves_start_time(self):
        """Re-registering an existing job must keep the original start_time."""
        original_time = "2026-01-01T00:00:00"
        self._register(job_id="job-upd", extra={"start_time": original_time})

        # Update with a new start_time that should be ignored
        self.client.post(
            "/servers/job-upd",
            data=json.dumps({"host": "node-02", "port": "9999",
                             "start_time": "2099-01-01T00:00:00"}),
            content_type="application/json",
        )

        body = self._json(self.client.get("/servers/job-upd"))
        self.assertEqual(body["start_time"], original_time)

    def test_update_refreshes_last_updated(self):
        self._register(job_id="job-ts")
        first_ts = rs.servers["job-ts"]["last_updated"]
        time.sleep(0.05)
        self._register(job_id="job-ts")
        second_ts = rs.servers["job-ts"]["last_updated"]
        self.assertGreater(second_ts, first_ts)

    def test_registry_file_written_on_register(self):
        self._register(job_id="persist-1")
        self.assertTrue(os.path.getsize(self.registry_file) > 0)
        with open(self.registry_file) as f:
            data = json.load(f)
        self.assertIn("persist-1", data)


# ===========================================================================
# Listing servers
# ===========================================================================
class TestListServers(RegistryTestBase):

    def test_empty_list(self):
        body = self._json(self.client.get("/servers"))
        self.assertEqual(body["count"], 0)
        self.assertEqual(body["servers"], [])

    def test_list_returns_all_servers(self):
        self._register(job_id="j1")
        self._register(job_id="j2")
        body = self._json(self.client.get("/servers"))
        self.assertEqual(body["count"], 2)
        ids = {s["job_id"] for s in body["servers"]}
        self.assertEqual(ids, {"j1", "j2"})

    def test_list_includes_job_id_field(self):
        self._register(job_id="j-id-check")
        servers = self._json(self.client.get("/servers"))["servers"]
        self.assertIn("job_id", servers[0])

    def test_list_includes_uptime_hours(self):
        self._register(job_id="j-uptime")
        servers = self._json(self.client.get("/servers"))["servers"]
        self.assertIn("uptime_hours", servers[0])
        self.assertGreaterEqual(servers[0]["uptime_hours"], 0)

    def test_list_sorted_newest_first(self):
        now = datetime.now()
        rs.servers["older"] = {
            "host": "n1", "port": "1", "start_time": (now - timedelta(hours=2)).isoformat(),
            "last_updated": now.isoformat(),
        }
        rs.servers["newer"] = {
            "host": "n2", "port": "2", "start_time": now.isoformat(),
            "last_updated": now.isoformat(),
        }
        servers = self._json(self.client.get("/servers"))["servers"]
        self.assertEqual(servers[0]["job_id"], "newer")
        self.assertEqual(servers[1]["job_id"], "older")


# ===========================================================================
# Get specific server
# ===========================================================================
class TestGetServer(RegistryTestBase):

    def test_get_existing_server(self):
        self._register(job_id="specific")
        r = self.client.get("/servers/specific")
        self.assertEqual(r.status_code, 200)
        body = self._json(r)
        self.assertEqual(body["host"], "node-01")

    def test_get_nonexistent_returns_404(self):
        r = self.client.get("/servers/does-not-exist")
        self.assertEqual(r.status_code, 404)
        self.assertIn("error", self._json(r))


# ===========================================================================
# Filter by owner
# ===========================================================================
class TestFilterByOwner(RegistryTestBase):

    def setUp(self):
        super().setUp()
        self._register(job_id="alice-1", owner="alice")
        self._register(job_id="alice-2", owner="alice")
        self._register(job_id="bob-1",   owner="bob")

    def test_filter_returns_only_matching_owner(self):
        body = self._json(self.client.get("/servers/by-owner/alice"))
        self.assertEqual(body["count"], 2)
        self.assertIn("alice-1", body["servers"])
        self.assertIn("alice-2", body["servers"])
        self.assertNotIn("bob-1", body["servers"])

    def test_filter_unknown_owner_returns_empty(self):
        body = self._json(self.client.get("/servers/by-owner/nobody"))
        self.assertEqual(body["count"], 0)
        self.assertEqual(body["servers"], {})


# ===========================================================================
# Deletion
# ===========================================================================
class TestDeletion(RegistryTestBase):

    def test_delete_existing_server(self):
        self._register(job_id="del-me")
        r = self.client.delete("/servers/del-me")
        self.assertEqual(r.status_code, 200)
        body = self._json(r)
        self.assertEqual(body["status"], "deleted")
        self.assertEqual(body["job_id"], "del-me")

    def test_delete_removes_server_from_list(self):
        self._register(job_id="del-gone")
        self.client.delete("/servers/del-gone")
        body = self._json(self.client.get("/servers"))
        self.assertEqual(body["count"], 0)

    def test_delete_nonexistent_returns_404(self):
        r = self.client.delete("/servers/ghost")
        self.assertEqual(r.status_code, 404)

    def test_delete_updates_registry_file(self):
        self._register(job_id="file-del")
        self.client.delete("/servers/file-del")
        with open(self.registry_file) as f:
            data = json.load(f)
        self.assertNotIn("file-del", data)


# ===========================================================================
# Dashboard
# ===========================================================================
class TestDashboard(RegistryTestBase):

    def test_dashboard_returns_200(self):
        r = self.client.get("/")
        self.assertEqual(r.status_code, 200)

    def test_dashboard_serves_html_file_when_present(self):
        r = self.client.get("/")
        self.assertIn(b"html", r.data.lower())

    def test_dashboard_fallback_when_file_missing(self):
        """When dashboard.html is absent, serve the inline fallback."""
        original = rs.__file__
        with patch("os.path.exists", return_value=False):
            r = self.client.get("/")
        self.assertEqual(r.status_code, 200)
        self.assertIn(b"Llama Server Registry", r.data)


# ===========================================================================
# Persistence  (save / load)
# ===========================================================================
class TestPersistence(RegistryTestBase):

    def test_load_registry_reads_saved_data(self):
        self._register(job_id="saved-job")
        # Wipe in-memory state and reload from disk
        rs.servers = {}
        rs.load_registry()
        self.assertIn("saved-job", rs.servers)

    def test_load_registry_handles_missing_file(self):
        """load_registry must not raise when the file doesn't exist yet."""
        rs.REGISTRY_FILE = "/tmp/nonexistent_registry_xyz.json"
        try:
            rs.load_registry()   # should not raise
        finally:
            rs.REGISTRY_FILE = self.registry_file

    def test_load_registry_handles_corrupt_file(self):
        """load_registry must not crash on invalid JSON."""
        with open(self.registry_file, "w") as f:
            f.write("NOT JSON {{{")
        rs.load_registry()  # should not raise
        self.assertEqual(rs.servers, {})  # servers stays empty / resets


# ===========================================================================
# Stale-server cleanup
# ===========================================================================
class TestCleanup(RegistryTestBase):

    def test_cleanup_removes_old_servers(self):
        """Servers older than MAX_AGE_HOURS should be purged."""
        cutoff = datetime.now() - timedelta(hours=rs.MAX_AGE_HOURS + 1)
        rs.servers["stale"] = {
            "host": "n1", "port": "1",
            "last_updated": cutoff.isoformat(),
        }
        rs.servers["fresh"] = {
            "host": "n2", "port": "2",
            "last_updated": datetime.now().isoformat(),
        }

        # Run one cleanup cycle directly (bypass the sleep loop)
        cutoff_str = (datetime.now() - timedelta(hours=rs.MAX_AGE_HOURS)).isoformat()
        with rs.lock:
            old_keys = [k for k, v in rs.servers.items()
                        if v.get("last_updated", "") < cutoff_str]
            for k in old_keys:
                del rs.servers[k]
            rs.save_registry()

        self.assertNotIn("stale", rs.servers)
        self.assertIn("fresh", rs.servers)


# ===========================================================================
# Concurrency  (basic thread-safety smoke test)
# ===========================================================================
class TestConcurrency(RegistryTestBase):

    def test_concurrent_registrations_are_safe(self):
        errors = []

        def register(i):
            try:
                with rs.app.test_client() as c:
                    c.post(
                        f"/servers/job-{i}",
                        data=json.dumps({"host": "n", "port": str(i)}),
                        content_type="application/json",
                    )
            except Exception as e:
                errors.append(e)

        threads = [threading.Thread(target=register, args=(i,)) for i in range(20)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(errors, [], f"Thread errors: {errors}")
        self.assertEqual(len(rs.servers), 20)


if __name__ == "__main__":
    unittest.main(verbosity=2)

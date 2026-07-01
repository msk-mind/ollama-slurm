package e2e

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func waitForJob(t *testing.T, svc *service.Service, jobID string, timeout time.Duration) types.Job {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := svc.GetJob(context.Background(), jobID)
		if err != nil {
			t.Fatalf("get job %s: %v", jobID, err)
		}
		if job.State == types.JobStateSucceeded && job.Result != nil {
			return job
		}
		if job.State == types.JobStateFailed {
			t.Fatalf("job %s failed: %s", jobID, job.ResultError)
		}
		time.Sleep(100 * time.Millisecond)
	}

	job, err := svc.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get job %s after timeout: %v", jobID, err)
	}
	t.Fatalf("job %s did not complete within %s; state=%q", jobID, timeout, job.State)
	return types.Job{}
}

func hasArtifact(artifacts []types.Artifact, artifactID string) bool {
	for _, artifact := range artifacts {
		if artifact.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

func writeTestFile(t *testing.T, path string, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func startFakeOpenAIServer(t *testing.T, repoRoot string) (string, string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}

	countFile := filepath.Join(t.TempDir(), "fake-count.txt")
	cmd := exec.Command(
		"/usr/bin/python3",
		filepath.Join(repoRoot, "tests", "e2e", "fake_openai_server.py"),
		"--listen-host", "127.0.0.1",
		"--listen-port", strconv.Itoa(port),
		"--count-file", countFile,
	)
	stderrPath := filepath.Join(t.TempDir(), "fake-openai.stderr")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create fake server stderr file: %v", err)
	}
	defer stderrFile.Close()
	cmd.Stdout = io.Discard
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake openai server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return baseURL, countFile
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	stderrBytes, _ := os.ReadFile(stderrPath)
	t.Fatalf("fake openai server did not become healthy on %s: %s", baseURL, strings.TrimSpace(string(stderrBytes)))
	return "", ""
}

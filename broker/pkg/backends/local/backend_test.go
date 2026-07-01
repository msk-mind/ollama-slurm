package local

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func TestGetRunFromHeartbeatCompleted(t *testing.T) {
	runRoot := t.TempDir()
	runDir := filepath.Join(runRoot, "job_123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "heartbeat.json"), []byte(`{"state":"completed","phase":"completed"}`), 0o644); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	backend := NewBackend(config.Config{
		LocalMode:       "command",
		RunRootPath:     runRoot,
		LocalScriptPath: "deploy/local/broker_worker.sh",
	})
	status, err := backend.GetRun(context.Background(), "job_123")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if status.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded, got %q", status.State)
	}
}

func TestGetRunRunningFromPID(t *testing.T) {
	runRoot := t.TempDir()
	runDir := filepath.Join(runRoot, "job_123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "local.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	backend := NewBackend(config.Config{
		LocalMode:       "command",
		RunRootPath:     runRoot,
		LocalScriptPath: "deploy/local/broker_worker.sh",
	})
	status, err := backend.GetRun(context.Background(), "job_123")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if status.State != types.JobStateRunning {
		t.Fatalf("expected running, got %q", status.State)
	}
}

func TestSubmitRunStubMode(t *testing.T) {
	backend := NewBackend(config.Config{LocalMode: "stub"})
	resp, err := backend.SubmitRun(context.Background(), types.Job{TaskType: "document_summary"})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if resp.BackendKind != "local" {
		t.Fatalf("expected local backend kind, got %q", resp.BackendKind)
	}
	if resp.InitialState != types.JobStateQueued {
		t.Fatalf("expected queued, got %q", resp.InitialState)
	}
}

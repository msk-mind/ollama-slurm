package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/limr/ollama-slurm/broker/pkg/backends"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type Backend struct {
	counter atomic.Uint64
	mode    string
	cfg     config.Config
}

type heartbeat struct {
	State string `json:"state"`
	Phase string `json:"phase"`
}

func NewBackend(cfg config.Config) *Backend {
	return &Backend{
		mode: cfg.LocalMode,
		cfg:  cfg,
	}
}

func (b *Backend) Name() string {
	return "local"
}

func (b *Backend) SubmitRun(_ context.Context, job types.Job) (backends.SubmitResponse, error) {
	if b.mode != "command" {
		runID := fmt.Sprintf("local-%06d", b.counter.Add(1))
		return backends.SubmitResponse{
			BackendKind:  b.Name(),
			BackendRunID: runID,
			InitialState: types.JobStateQueued,
		}, nil
	}

	runRoot := brokerRunRoot(job)
	repoRoot := brokerRepoRoot(job)
	runID := job.ID
	outputDir := filepath.Join(runRoot, runID)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return backends.SubmitResponse{}, fmt.Errorf("create output dir: %w", err)
	}

	scriptPath := resolvePath(repoRoot, b.cfg.LocalScriptPath)
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"BROKER_JOB_ID="+job.ID,
		"BROKER_TASK_TYPE="+job.TaskType,
		"BROKER_REPO_ROOT="+repoRoot,
		"BROKER_OUTPUT_DIR="+outputDir,
		"BROKER_OUTPUT_SCHEMA="+job.Request.OutputSchema.Name,
	)

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return backends.SubmitResponse{}, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	if err := cmd.Start(); err != nil {
		return backends.SubmitResponse{}, fmt.Errorf("start local worker: %w", err)
	}

	if err := os.WriteFile(filepath.Join(outputDir, "local.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return backends.SubmitResponse{}, fmt.Errorf("write pid file: %w", err)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return backends.SubmitResponse{
		BackendKind:  b.Name(),
		BackendRunID: runID,
		InitialState: types.JobStateDispatching,
	}, nil
}

func (b *Backend) GetRun(_ context.Context, backendRunID string) (backends.RunStatus, error) {
	if b.mode != "command" {
		return backends.RunStatus{
			BackendRunID: backendRunID,
			State:        types.JobStateQueued,
			RawState:     "STUB",
		}, nil
	}

	runDir := filepath.Join(b.cfg.RunRootPath, backendRunID)
	if hb, err := readHeartbeat(filepath.Join(runDir, "heartbeat.json")); err == nil {
		if state, raw := mapHeartbeatState(hb.State); state != "" {
			return backends.RunStatus{
				BackendRunID: backendRunID,
				State:        state,
				RawState:     raw,
			}, nil
		}
	}

	pid, err := readPID(filepath.Join(runDir, "local.pid"))
	if err == nil && processAlive(pid) {
		return backends.RunStatus{
			BackendRunID: backendRunID,
			State:        types.JobStateRunning,
			RawState:     "RUNNING",
		}, nil
	}

	if _, err := os.Stat(filepath.Join(runDir, "result.json")); err == nil {
		return backends.RunStatus{
			BackendRunID: backendRunID,
			State:        types.JobStateSucceeded,
			RawState:     "COMPLETED",
		}, nil
	}

	if err == nil {
		return backends.RunStatus{
			BackendRunID: backendRunID,
			State:        types.JobStateFailed,
			RawState:     "EXITED",
		}, nil
	}

	return backends.RunStatus{
		BackendRunID: backendRunID,
		State:        types.JobStateQueued,
		RawState:     "PENDING",
	}, nil
}

func (b *Backend) CancelRun(_ context.Context, backendRunID string) error {
	if b.mode != "command" {
		return nil
	}

	pid, err := readPID(filepath.Join(b.cfg.RunRootPath, backendRunID, "local.pid"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read pid file: %w", err)
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate local worker pid %d: %w", pid, err)
	}
	return nil
}

func resolvePath(repoRoot, candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(repoRoot, candidate)
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}
	return pid, nil
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func readHeartbeat(path string) (heartbeat, error) {
	var hb heartbeat
	data, err := os.ReadFile(path)
	if err != nil {
		return hb, err
	}
	if err := json.Unmarshal(data, &hb); err != nil {
		return hb, err
	}
	return hb, nil
}

func mapHeartbeatState(state string) (types.JobState, string) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running":
		return types.JobStateRunning, "RUNNING"
	case "completed":
		return types.JobStateSucceeded, "COMPLETED"
	case "failed":
		return types.JobStateFailed, "FAILED"
	case "cancelled":
		return types.JobStateCancelled, "CANCELLED"
	default:
		return "", ""
	}
}

func brokerRunRoot(job types.Job) string {
	if job.Request.TaskParams != nil {
		if value, ok := job.Request.TaskParams["_broker_run_root"].(string); ok && value != "" {
			return value
		}
	}
	return ".broker/runs"
}

func brokerRepoRoot(job types.Job) string {
	if job.Request.TaskParams != nil {
		if value, ok := job.Request.TaskParams["_broker_repo_root"].(string); ok && value != "" {
			return value
		}
	}
	return "."
}

package slurm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type fakeRunner struct {
	outputs  map[string][]byte
	errors   map[string]error
	lastArgs []string
}

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.lastArgs = append([]string(nil), args...)
	if len(f.outputs) == 0 && len(f.errors) == 0 {
		return nil, nil
	}
	key := ""
	if len(args) > 0 {
		key = args[0]
	}
	return f.outputs[key], f.errors[key]
}

func TestParseSlurmState(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want types.JobState
	}{
		{name: "pending", in: "PENDING\n", want: types.JobStateQueued},
		{name: "running", in: "RUNNING\n", want: types.JobStateRunning},
		{name: "completed", in: "COMPLETED\n", want: types.JobStateSucceeded},
		{name: "cancelled", in: "CANCELLED by 123\n", want: types.JobStateCancelled},
		{name: "timeout", in: "TIMEOUT\n", want: types.JobStateTimedOut},
		{name: "preempted", in: "PREEMPTED\n", want: types.JobStatePreempted},
		{name: "failed", in: "FAILED\n", want: types.JobStateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSlurmState([]byte(tt.in)); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestSubmitRunCommandMode(t *testing.T) {
	cfg := config.Config{
		SlurmMode:       "command",
		SlurmSubmitCmd:  "sbatch",
		SlurmScriptPath: "deploy/slurm/broker_worker.slurm",
	}
	backend := NewBackendWithRunner(cfg, fakeRunner{
		outputs: map[string][]byte{"--parsable": []byte("12345\n")},
	})

	resp, err := backend.SubmitRun(context.Background(), types.Job{TaskType: "log_analysis"})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if resp.BackendRunID != "12345" {
		t.Fatalf("expected backend run id 12345, got %q", resp.BackendRunID)
	}
}

func TestSubmitRunCommandModeError(t *testing.T) {
	cfg := config.Config{
		SlurmMode:       "command",
		SlurmSubmitCmd:  "sbatch",
		SlurmScriptPath: "deploy/slurm/broker_worker.slurm",
	}
	backend := NewBackendWithRunner(cfg, fakeRunner{
		outputs: map[string][]byte{"--parsable": []byte("boom")},
		errors:  map[string]error{"--parsable": errors.New("exit 1")},
	})

	if _, err := backend.SubmitRun(context.Background(), types.Job{TaskType: "log_analysis"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetRunCommandMode(t *testing.T) {
	cfg := config.Config{
		SlurmMode:      "command",
		SlurmStatusCmd: "sacct",
	}
	backend := NewBackendWithRunner(cfg, fakeRunner{
		outputs: map[string][]byte{"--jobs": []byte("RUNNING|0:0\n")},
	})

	status, err := backend.GetRun(context.Background(), "12345")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if status.State != types.JobStateRunning {
		t.Fatalf("expected running, got %q", status.State)
	}
	if status.RawState != "RUNNING" {
		t.Fatalf("expected raw state RUNNING, got %q", status.RawState)
	}
}

func TestGetRunCommandModeArrayChildMatchesExactTask(t *testing.T) {
	cfg := config.Config{
		SlurmMode:      "command",
		SlurmStatusCmd: "sacct",
	}
	backend := NewBackendWithRunner(cfg, fakeRunner{
		outputs: map[string][]byte{
			"--jobs": []byte("98765|COMPLETED|0:0\n98765_0|FAILED|1:0\n98765_1|RUNNING|0:0\n"),
		},
	})

	status, err := backend.GetRun(context.Background(), "98765_1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if status.State != types.JobStateRunning || status.RawState != "RUNNING" || status.ExitCode != "0:0" {
		t.Fatalf("unexpected array child status: %#v", status)
	}
}

func TestSubmitRunAddsDependencyArgs(t *testing.T) {
	cfg := config.Config{
		SlurmMode:       "command",
		SlurmSubmitCmd:  "sbatch",
		SlurmScriptPath: "deploy/slurm/broker_worker.slurm",
	}
	runner := &recordingRunner{
		output: []byte("12345\n"),
	}
	backend := NewBackendWithRunner(cfg, runner)

	_, err := backend.SubmitRun(context.Background(), types.Job{
		TaskType: "repo_summary",
		Request: types.SubmitJobRequest{
			TaskParams: map[string]any{
				"_dependency_backend_run_ids": []string{"111", "222"},
			},
		},
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if len(runner.args) < 3 || runner.args[0] != "--parsable" || runner.args[1] != "--job-name" || runner.args[2] != "broker-repo_summary" {
		t.Fatalf("unexpected submit prefix: %#v", runner.args)
	}
	if argValueAfter(runner.args, "--dependency") != "afterany:111:222" {
		t.Fatalf("expected dependency arg, got %#v", runner.args)
	}
}

func TestSubmitRunAddsQOSArg(t *testing.T) {
	cfg := config.Config{
		SlurmMode:       "command",
		SlurmSubmitCmd:  "sbatch",
		SlurmScriptPath: "deploy/slurm/broker_worker.slurm",
	}
	runner := &recordingRunner{
		output: []byte("12345\n"),
	}
	backend := NewBackendWithRunner(cfg, runner)

	_, err := backend.SubmitRun(context.Background(), types.Job{
		TaskType: "rag_compress",
		Request: types.SubmitJobRequest{
			ExecutionProfile: types.ExecutionProfile{
				QOS: "scavenger",
			},
		},
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if argValueAfter(runner.args, "--qos") != "scavenger" {
		t.Fatalf("expected --qos scavenger, got %#v", runner.args)
	}
}

func TestSubmitRunAddsPartitionFromTier(t *testing.T) {
	cfg := config.Config{
		SlurmMode:          "command",
		SlurmSubmitCmd:     "sbatch",
		SlurmScriptPath:    "deploy/slurm/broker_worker.slurm",
		SlurmPartitionA100: "a100",
	}
	runner := &recordingRunner{
		output: []byte("12345\n"),
	}
	backend := NewBackendWithRunner(cfg, runner)

	_, err := backend.SubmitRun(context.Background(), types.Job{
		TaskType: "rag_compress",
		Request: types.SubmitJobRequest{
			ExecutionProfile: types.ExecutionProfile{
				Tier: "a100-reasoning",
			},
		},
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if argValueAfter(runner.args, "--partition") != "a100" {
		t.Fatalf("expected --partition a100, got %#v", runner.args)
	}
}

func TestSubmitRunAddsNodeListAndConstraintFromTierDefaults(t *testing.T) {
	cfg := config.Config{
		SlurmMode:          "command",
		SlurmSubmitCmd:     "sbatch",
		SlurmScriptPath:    "deploy/slurm/broker_worker.slurm",
		SlurmNodeListP40:   "pllimsksparky[1-4]",
		SlurmConstraintP40: "p40",
	}
	runner := &recordingRunner{
		output: []byte("12345\n"),
	}
	backend := NewBackendWithRunner(cfg, runner)

	_, err := backend.SubmitRun(context.Background(), types.Job{
		TaskType: "rag_compress",
		Request: types.SubmitJobRequest{
			ExecutionProfile: types.ExecutionProfile{
				Tier: "p40-rag-compression",
			},
		},
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if argValueAfter(runner.args, "--nodelist") != "pllimsksparky[1-4]" {
		t.Fatalf("expected --nodelist pllimsksparky[1-4], got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--nodes") != "1" {
		t.Fatalf("expected --nodes 1, got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--ntasks") != "1" {
		t.Fatalf("expected --ntasks 1, got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--constraint") != "p40" {
		t.Fatalf("expected --constraint p40, got %#v", runner.args)
	}
}

func TestSubmitRunPrefersExplicitNodeListAndConstraintOverrides(t *testing.T) {
	cfg := config.Config{
		SlurmMode:          "command",
		SlurmSubmitCmd:     "sbatch",
		SlurmScriptPath:    "deploy/slurm/broker_worker.slurm",
		SlurmNodeListP40:   "pllimsksparky[1-4]",
		SlurmConstraintP40: "p40",
	}
	runner := &recordingRunner{
		output: []byte("12345\n"),
	}
	backend := NewBackendWithRunner(cfg, runner)

	_, err := backend.SubmitRun(context.Background(), types.Job{
		TaskType: "rag_compress",
		Request: types.SubmitJobRequest{
			ExecutionProfile: types.ExecutionProfile{
				Tier:       "p40-rag-compression",
				NodeList:   "pllimsksparky2",
				Constraint: "gpu24g",
			},
		},
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if argValueAfter(runner.args, "--nodelist") != "pllimsksparky2" {
		t.Fatalf("expected explicit --nodelist override, got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--nodes") != "1" {
		t.Fatalf("expected --nodes 1, got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--ntasks") != "1" {
		t.Fatalf("expected --ntasks 1, got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--constraint") != "gpu24g" {
		t.Fatalf("expected explicit --constraint override, got %#v", runner.args)
	}
}

func TestSubmitRunBatchCommandMode(t *testing.T) {
	runRoot := t.TempDir()
	cfg := config.Config{
		SlurmMode:       "command",
		SlurmSubmitCmd:  "sbatch",
		SlurmScriptPath: "deploy/slurm/broker_worker.slurm",
	}
	runner := &recordingRunner{
		output: []byte("98765\n"),
	}
	backend := NewBackendWithRunner(cfg, runner)

	jobs := []types.Job{
		{
			ID:        "job_a",
			TaskType:  "repo_summary",
			RootJobID: "root_batch_1",
			Request: types.SubmitJobRequest{
				TaskParams:   map[string]any{"_broker_run_root": runRoot, "_broker_repo_root": "/repo"},
				OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
			},
		},
		{
			ID:        "job_b",
			TaskType:  "repo_summary",
			RootJobID: "root_batch_1",
			Request: types.SubmitJobRequest{
				TaskParams:   map[string]any{"_broker_run_root": runRoot, "_broker_repo_root": "/repo"},
				OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
			},
		},
	}

	resp, err := backend.SubmitRunBatch(context.Background(), jobs)
	if err != nil {
		t.Fatalf("submit run batch: %v", err)
	}
	if len(resp) != 2 || resp[0].BackendRunID != "98765_0" || resp[1].BackendRunID != "98765_1" {
		t.Fatalf("unexpected batch responses: %#v", resp)
	}
	if !containsArg(runner.args, "--array") || !containsArg(runner.args, "0-1") {
		t.Fatalf("expected sbatch array args, got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--nodes") != "1" {
		t.Fatalf("expected --nodes 1 for arrays, got %#v", runner.args)
	}
	if argValueAfter(runner.args, "--ntasks") != "1" {
		t.Fatalf("expected --ntasks 1 for arrays, got %#v", runner.args)
	}
	exportArg := argValueAfter(runner.args, "--export")
	if !strings.Contains(exportArg, "BROKER_ARRAY_MANIFEST=") {
		t.Fatalf("expected manifest export, got %q", exportArg)
	}
	manifestPath := strings.TrimPrefix(findPart(exportArg, "BROKER_ARRAY_MANIFEST="), "BROKER_ARRAY_MANIFEST=")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifestBytes), "\"broker_job_id\": \"job_a\"") {
		t.Fatalf("unexpected manifest: %s", string(manifestBytes))
	}
	if filepath.Dir(manifestPath) != filepath.Join(runRoot, "_slurm_arrays") {
		t.Fatalf("unexpected manifest dir: %s", manifestPath)
	}
}

func TestCancelRunUsesExactArrayChildID(t *testing.T) {
	cfg := config.Config{
		SlurmMode:      "command",
		SlurmCancelCmd: "scancel",
	}
	runner := &recordingRunner{}
	backend := NewBackendWithRunner(cfg, runner)

	if err := backend.CancelRun(context.Background(), "98765_3"); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if len(runner.args) != 1 || runner.args[0] != "98765_3" {
		t.Fatalf("expected exact array child cancel target, got %#v", runner.args)
	}
}

func TestParseSqueueStateMatchesArrayChild(t *testing.T) {
	runRef := parseRunRef("98765_4")
	rawState := parseSqueueState([]byte("98765|RUNNING\n98765_4|FAILED\n"), runRef)
	if rawState != "FAILED" {
		t.Fatalf("expected FAILED, got %q", rawState)
	}
}

type recordingRunner struct {
	args   []string
	output []byte
	err    error
}

func (r *recordingRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.args = append([]string(nil), args...)
	return r.output, r.err
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func argValueAfter(args []string, key string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	return ""
}

func findPart(value, prefix string) string {
	for _, part := range strings.Split(value, ",") {
		if strings.HasPrefix(part, prefix) {
			return part
		}
	}
	return ""
}

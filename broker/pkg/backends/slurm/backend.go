package slurm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/limr/ollama-slurm/broker/pkg/backends"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type Backend struct {
	counter atomic.Uint64
	mode    string
	runner  commandRunner
	cfg     config.Config
}

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

func NewBackend(cfg config.Config) *Backend {
	return &Backend{
		mode:   cfg.SlurmMode,
		runner: execRunner{},
		cfg:    cfg,
	}
}

func NewBackendWithRunner(cfg config.Config, runner commandRunner) *Backend {
	return &Backend{
		mode:   cfg.SlurmMode,
		runner: runner,
		cfg:    cfg,
	}
}

func (b *Backend) Name() string {
	return "slurm"
}

func (b *Backend) SubmitRun(ctx context.Context, job types.Job) (backends.SubmitResponse, error) {
	if b.mode != "command" {
		runID := fmt.Sprintf("slurm-%06d", b.counter.Add(1))
		return backends.SubmitResponse{
			BackendKind:  b.Name(),
			BackendRunID: runID,
			InitialState: types.JobStateQueued,
		}, nil
	}

	args := []string{
		"--parsable",
		"--job-name", "broker-" + job.TaskType,
	}
	if partition := strings.TrimSpace(selectPartition(job.Request.ExecutionProfile.Tier, b.cfg)); partition != "" {
		args = append(args, "--partition", partition)
	}
	if qos := strings.TrimSpace(job.Request.ExecutionProfile.QOS); qos != "" {
		args = append(args, "--qos", qos)
	}
	if nodelist := strings.TrimSpace(selectNodeList(job.Request.ExecutionProfile, b.cfg)); nodelist != "" {
		args = append(args, "--nodelist", nodelist)
	}
	args = append(args, singleWorkerSchedulingArgs()...)
	if constraint := strings.TrimSpace(selectConstraint(job.Request.ExecutionProfile, b.cfg)); constraint != "" {
		args = append(args, "--constraint", constraint)
	}
	if dependencyArg := buildDependencyArg(job); dependencyArg != "" {
		args = append(args, "--dependency", dependencyArg)
	}
	args = append(args,
		"--export", buildExport(job),
		b.cfg.SlurmScriptPath,
	)
	output, err := b.runner.Run(ctx, b.cfg.SlurmSubmitCmd, args...)
	if err != nil {
		return backends.SubmitResponse{}, fmt.Errorf("submit slurm job: %w: %s", err, strings.TrimSpace(string(output)))
	}
	jobID := strings.TrimSpace(string(output))
	if jobID == "" {
		return backends.SubmitResponse{}, errors.New("empty slurm job id from submit command")
	}

	return backends.SubmitResponse{
		BackendKind:  b.Name(),
		BackendRunID: jobID,
		InitialState: types.JobStateQueued,
	}, nil
}

func (b *Backend) SubmitRunBatch(ctx context.Context, jobs []types.Job) ([]backends.SubmitResponse, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	if b.mode != "command" {
		responses := make([]backends.SubmitResponse, 0, len(jobs))
		for range jobs {
			runID := fmt.Sprintf("slurm-%06d", b.counter.Add(1))
			responses = append(responses, backends.SubmitResponse{
				BackendKind:  b.Name(),
				BackendRunID: runID,
				InitialState: types.JobStateQueued,
			})
		}
		return responses, nil
	}

	manifestPath, err := writeArrayManifest(jobs)
	if err != nil {
		return nil, fmt.Errorf("write array manifest: %w", err)
	}

	args := []string{
		"--parsable",
		"--job-name", "broker-" + jobs[0].TaskType + "-batch",
		"--array", fmt.Sprintf("0-%d", len(jobs)-1),
	}
	if partition := strings.TrimSpace(selectPartition(jobs[0].Request.ExecutionProfile.Tier, b.cfg)); partition != "" {
		args = append(args, "--partition", partition)
	}
	if qos := strings.TrimSpace(jobs[0].Request.ExecutionProfile.QOS); qos != "" {
		args = append(args, "--qos", qos)
	}
	if nodelist := strings.TrimSpace(selectNodeList(jobs[0].Request.ExecutionProfile, b.cfg)); nodelist != "" {
		args = append(args, "--nodelist", nodelist)
	}
	args = append(args, singleWorkerSchedulingArgs()...)
	if constraint := strings.TrimSpace(selectConstraint(jobs[0].Request.ExecutionProfile, b.cfg)); constraint != "" {
		args = append(args, "--constraint", constraint)
	}
	args = append(args,
		"--export", buildBatchExport(jobs[0], manifestPath),
		b.cfg.SlurmScriptPath,
	)
	output, err := b.runner.Run(ctx, b.cfg.SlurmSubmitCmd, args...)
	if err != nil {
		return nil, fmt.Errorf("submit slurm job array: %w: %s", err, strings.TrimSpace(string(output)))
	}
	arrayJobID := strings.TrimSpace(string(output))
	if arrayJobID == "" {
		return nil, errors.New("empty slurm array job id from submit command")
	}

	responses := make([]backends.SubmitResponse, 0, len(jobs))
	for i := range jobs {
		responses = append(responses, backends.SubmitResponse{
			BackendKind:  b.Name(),
			BackendRunID: fmt.Sprintf("%s_%d", arrayJobID, i),
			InitialState: types.JobStateQueued,
		})
	}
	return responses, nil
}

func (b *Backend) GetRun(ctx context.Context, backendRunID string) (backends.RunStatus, error) {
	if b.mode != "command" {
		return backends.RunStatus{
			BackendRunID: backendRunID,
			State:        types.JobStateQueued,
			RawState:     "STUB",
		}, nil
	}

	runRef := parseRunRef(backendRunID)

	output, err := b.runner.Run(
		ctx,
		b.cfg.SlurmStatusCmd,
		"--jobs", runRef.queryID,
		"--noheader",
		"--parsable2",
		"--format", "JobIDRaw,State,ExitCode",
	)
	if err != nil {
		return b.getRunFromSqueue(ctx, runRef, err, output)
	}

	state, rawState, exitCode := parseSlurmStatus(output, runRef)
	return backends.RunStatus{
		BackendRunID: backendRunID,
		State:        state,
		RawState:     rawState,
		ExitCode:     exitCode,
	}, nil
}

func (b *Backend) CancelRun(ctx context.Context, backendRunID string) error {
	if b.mode != "command" {
		return nil
	}
	runRef := parseRunRef(backendRunID)
	output, err := b.runner.Run(ctx, b.cfg.SlurmCancelCmd, runRef.queryID)
	if err != nil {
		return fmt.Errorf("cancel slurm job: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func parseSlurmState(output []byte) types.JobState {
	state := strings.ToUpper(strings.TrimSpace(string(output)))
	switch {
	case strings.Contains(state, "PENDING"):
		return types.JobStateQueued
	case strings.Contains(state, "RUNNING"):
		return types.JobStateRunning
	case strings.Contains(state, "COMPLETED"):
		return types.JobStateSucceeded
	case strings.Contains(state, "CANCELLED"):
		return types.JobStateCancelled
	case strings.Contains(state, "TIMEOUT"):
		return types.JobStateTimedOut
	case strings.Contains(state, "PREEMPTED"):
		return types.JobStatePreempted
	case strings.Contains(state, "FAILED"), strings.Contains(state, "OUT_OF_MEMORY"):
		return types.JobStateFailed
	default:
		return types.JobStateQueued
	}
}

func parseSlurmStatus(output []byte, runRef slurmRunRef) (types.JobState, string, string) {
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) == 2 {
			rawState := strings.TrimSpace(fields[0])
			exitCode := strings.TrimSpace(fields[1])
			return parseSlurmState([]byte(rawState)), rawState, exitCode
		}
		if len(fields) < 3 {
			continue
		}
		jobIDRaw := strings.TrimSpace(fields[0])
		rawState := strings.TrimSpace(fields[1])
		exitCode := strings.TrimSpace(fields[2])
		if runRef.matches(jobIDRaw) {
			return parseSlurmState([]byte(rawState)), rawState, exitCode
		}
	}
	return types.JobStateQueued, "", ""
}

func (b *Backend) getRunFromSqueue(ctx context.Context, runRef slurmRunRef, originalErr error, originalOutput []byte) (backends.RunStatus, error) {
	output, err := b.runner.Run(
		ctx,
		"squeue",
		"--jobs", runRef.queryID,
		"--noheader",
		"--format", "%i|%T",
	)
	if err != nil {
		return backends.RunStatus{}, fmt.Errorf(
			"get slurm status: %w: %s; fallback squeue failed: %v: %s",
			originalErr,
			strings.TrimSpace(string(originalOutput)),
			err,
			strings.TrimSpace(string(output)),
		)
	}

	rawState := parseSqueueState(output, runRef)
	return backends.RunStatus{
		BackendRunID: runRef.originalID,
		State:        parseSlurmState([]byte(rawState)),
		RawState:     rawState,
	}, nil
}

func parseSqueueState(output []byte, runRef slurmRunRef) string {
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "|", 2)
		if len(fields) != 2 {
			continue
		}
		jobIDRaw := strings.TrimSpace(fields[0])
		rawState := strings.TrimSpace(fields[1])
		if runRef.matches(jobIDRaw) {
			return rawState
		}
	}
	return strings.TrimSpace(string(output))
}

func buildExport(job types.Job) string {
	return strings.Join(buildExportParts(job), ",")
}

func buildBatchExport(job types.Job, manifestPath string) string {
	parts := []string{
		"ALL",
		"BROKER_ARRAY_MANIFEST=" + manifestPath,
		"BROKER_REPO_ROOT=" + brokerRepoRoot(job),
	}
	return strings.Join(parts, ",")
}

func buildExportParts(job types.Job) []string {
	runRoot := strings.TrimRight(brokerRunRoot(job), "/")
	repoRoot := brokerRepoRoot(job)
	outputDir := fmt.Sprintf("%s/%s", runRoot, job.ID)
	parts := []string{
		"ALL",
		"BROKER_JOB_ID=" + job.ID,
		"BROKER_TASK_TYPE=" + job.TaskType,
		"BROKER_REPO_ROOT=" + repoRoot,
		"BROKER_OUTPUT_DIR=" + outputDir,
	}
	if job.Request.OutputSchema.Name != "" {
		parts = append(parts, "BROKER_OUTPUT_SCHEMA="+job.Request.OutputSchema.Name)
	}
	return parts
}

func writeArrayManifest(jobs []types.Job) (string, error) {
	runRoot := brokerRunRoot(jobs[0])
	manifestDir := filepath.Join(runRoot, "_slurm_arrays")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return "", err
	}

	manifestEntries := make([]map[string]string, 0, len(jobs))
	for _, job := range jobs {
		entry := map[string]string{
			"broker_job_id":    job.ID,
			"broker_task_type": job.TaskType,
			"broker_output_dir": filepath.Join(
				strings.TrimRight(brokerRunRoot(job), "/"),
				job.ID,
			),
		}
		if schema := strings.TrimSpace(job.Request.OutputSchema.Name); schema != "" {
			entry["broker_output_schema"] = schema
		}
		manifestEntries = append(manifestEntries, entry)
	}

	manifestBytes, err := json.MarshalIndent(manifestEntries, "", "  ")
	if err != nil {
		return "", err
	}
	manifestPath := filepath.Join(manifestDir, fmt.Sprintf("%s.json", jobs[0].RootJobID))
	if jobs[0].RootJobID == "" {
		manifestPath = filepath.Join(manifestDir, fmt.Sprintf("%s.json", jobs[0].ID))
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return "", err
	}
	return manifestPath, nil
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

func buildDependencyArg(job types.Job) string {
	ids := dependencyBackendRunIDs(job)
	if len(ids) == 0 {
		return ""
	}
	return "afterany:" + strings.Join(ids, ":")
}

func singleWorkerSchedulingArgs() []string {
	return []string{
		"--nodes", "1",
		"--ntasks", "1",
	}
}

func dependencyBackendRunIDs(job types.Job) []string {
	if job.Request.TaskParams == nil {
		return nil
	}
	raw, ok := job.Request.TaskParams["_dependency_backend_run_ids"]
	if !ok {
		return nil
	}
	items, ok := raw.([]string)
	if ok {
		return filterNonEmpty(items)
	}
	generic, ok := raw.([]any)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(generic))
	for _, item := range generic {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			ids = append(ids, strings.TrimSpace(text))
		}
	}
	return ids
}

func filterNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func selectPartition(tier string, cfg config.Config) string {
	switch strings.TrimSpace(tier) {
	case "cpu-rag-indexing":
		return cfg.SlurmPartitionCPU
	case "p40-rag-compression":
		return cfg.SlurmPartitionP40
	case "a100-reasoning":
		return cfg.SlurmPartitionA100
	default:
		return ""
	}
}

func selectNodeList(profile types.ExecutionProfile, cfg config.Config) string {
	if value := strings.TrimSpace(profile.NodeList); value != "" {
		return value
	}
	switch strings.TrimSpace(profile.Tier) {
	case "cpu-rag-indexing":
		return cfg.SlurmNodeListCPU
	case "p40-rag-compression":
		return cfg.SlurmNodeListP40
	case "a100-reasoning":
		return cfg.SlurmNodeListA100
	default:
		return ""
	}
}

func selectConstraint(profile types.ExecutionProfile, cfg config.Config) string {
	if value := strings.TrimSpace(profile.Constraint); value != "" {
		return value
	}
	switch strings.TrimSpace(profile.Tier) {
	case "cpu-rag-indexing":
		return cfg.SlurmConstraintCPU
	case "p40-rag-compression":
		return cfg.SlurmConstraintP40
	case "a100-reasoning":
		return cfg.SlurmConstraintA100
	default:
		return ""
	}
}

type slurmRunRef struct {
	originalID string
	queryID    string
	arrayJobID string
	taskID     string
}

func parseRunRef(backendRunID string) slurmRunRef {
	trimmed := strings.TrimSpace(backendRunID)
	parts := strings.SplitN(trimmed, "_", 2)
	ref := slurmRunRef{
		originalID: trimmed,
		queryID:    trimmed,
		arrayJobID: trimmed,
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		ref.arrayJobID = parts[0]
		ref.taskID = parts[1]
		ref.queryID = trimmed
	}
	return ref
}

func (r slurmRunRef) matches(jobIDRaw string) bool {
	candidate := strings.TrimSpace(jobIDRaw)
	if candidate == "" {
		return false
	}
	if candidate == r.queryID || candidate == r.originalID {
		return true
	}
	if r.taskID == "" {
		return candidate == r.arrayJobID
	}
	if candidate == r.arrayJobID+"_"+r.taskID || candidate == r.arrayJobID+"."+r.taskID {
		return true
	}
	return false
}

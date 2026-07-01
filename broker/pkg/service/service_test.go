package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/audit"
	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/backends"
	localbackend "github.com/limr/ollama-slurm/broker/pkg/backends/local"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type fakeBackend struct {
	status backends.RunStatus
}

func (f fakeBackend) Name() string { return "fake" }

func (f fakeBackend) SubmitRun(context.Context, types.Job) (backends.SubmitResponse, error) {
	return backends.SubmitResponse{
		BackendKind:  "fake",
		BackendRunID: "run-1",
		InitialState: types.JobStateQueued,
	}, nil
}

func (f fakeBackend) GetRun(context.Context, string) (backends.RunStatus, error) {
	return f.status, nil
}

func (f fakeBackend) CancelRun(context.Context, string) error { return nil }

type mutableFakeBackend struct {
	status backends.RunStatus
}

func (f *mutableFakeBackend) Name() string { return "fake" }

func (f *mutableFakeBackend) SubmitRun(context.Context, types.Job) (backends.SubmitResponse, error) {
	return backends.SubmitResponse{
		BackendKind:  "fake",
		BackendRunID: "run-1",
		InitialState: types.JobStateQueued,
	}, nil
}

func (f *mutableFakeBackend) GetRun(context.Context, string) (backends.RunStatus, error) {
	return f.status, nil
}

func (f *mutableFakeBackend) CancelRun(context.Context, string) error { return nil }

type fakeBatchBackend struct {
	status        backends.RunStatus
	batchCalls    int
	batchSizes    []int
	submittedJobs []types.Job
}

func (f *fakeBatchBackend) Name() string { return "fake-batch" }

func (f *fakeBatchBackend) SubmitRun(context.Context, types.Job) (backends.SubmitResponse, error) {
	return backends.SubmitResponse{
		BackendKind:  "fake-batch",
		BackendRunID: "single-run-1",
		InitialState: types.JobStateQueued,
	}, nil
}

func (f *fakeBatchBackend) SubmitRunBatch(_ context.Context, jobs []types.Job) ([]backends.SubmitResponse, error) {
	f.batchCalls++
	f.batchSizes = append(f.batchSizes, len(jobs))
	f.submittedJobs = append(f.submittedJobs, jobs...)
	responses := make([]backends.SubmitResponse, 0, len(jobs))
	for i := range jobs {
		responses = append(responses, backends.SubmitResponse{
			BackendKind:  "fake-batch",
			BackendRunID: "batch-run-" + string(rune('0'+i)),
			InitialState: types.JobStateQueued,
		})
	}
	return responses, nil
}

func (f *fakeBatchBackend) GetRun(context.Context, string) (backends.RunStatus, error) {
	return f.status, nil
}

func (f *fakeBatchBackend) CancelRun(context.Context, string) error { return nil }

func newServiceThrottledBatchFixture(t *testing.T, opts Options) (*Service, *fakeBatchBackend) {
	t.Helper()
	backend := &fakeBatchBackend{
		status: backends.RunStatus{State: types.JobStateQueued, RawState: "PENDING"},
	}
	svc := NewWithAuditAndOptions(
		store.NewMemoryJobStore(),
		backend,
		log.New(io.Discard, "", 0),
		audit.NewNopLogger(),
		t.TempDir(),
		".",
		opts,
	)
	return svc, backend
}

func newServiceRetryBudgetFixture(t *testing.T, retryBudget int) (*Service, *store.MemoryJobStore) {
	t.Helper()
	jobStore := store.NewMemoryJobStore()
	svc := NewWithAuditAndOptions(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		audit.NewNopLogger(),
		t.TempDir(),
		".",
		Options{RootActionMaxRetriedShards: retryBudget},
	)
	return svc, jobStore
}

func ctxAs(actor, role string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Actor: actor, Role: role})
}

func aliceUserCtx() context.Context {
	return ctxAs("alice", "user")
}

func bobUserCtx() context.Context {
	return ctxAs("bob", "user")
}

func adminCtx() context.Context {
	return ctxAs("admin", "admin")
}

func rootAdminCtx() context.Context {
	return ctxAs("root", "admin")
}

func seedFailedRetryRootJobs(t *testing.T, jobStore *store.MemoryJobStore, rootJobID, submittedBy string, count int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < count; i++ {
		job := types.Job{
			ID:          "job_failed_" + string(rune('1'+i)),
			TaskType:    "document_summary",
			State:       types.JobStateFailed,
			RootJobID:   rootJobID,
			SubmittedBy: submittedBy,
			Request:     types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
			Orchestration: &types.OrchestrationInfo{
				RootJobID: rootJobID, Strategy: "fanout_child", ShardIndex: i, ShardCount: count,
			},
			CreatedAt:   now.Add(time.Duration(i) * time.Second),
			UpdatedAt:   now.Add(time.Duration(i) * time.Second),
			SubmittedAt: now.Add(time.Duration(i) * time.Second),
		}
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
}

func serviceDocChildRequests(count int) []types.ParallelChildRequest {
	children := make([]types.ParallelChildRequest, 0, count)
	for i := 0; i < count; i++ {
		children = append(children, types.ParallelChildRequest{
			InputRefs:  []types.InputRef{{Type: "file", URI: "file:///tmp/doc-" + string(rune('a'+i)) + ".txt"}},
			ShardIndex: i,
			ShardCount: count,
		})
	}
	return children
}

func serviceFileChildRequests(count int) []types.ParallelChildRequest {
	children := make([]types.ParallelChildRequest, 0, count)
	for i := 0; i < count; i++ {
		children = append(children, types.ParallelChildRequest{
			InputRefs:  []types.InputRef{{Type: "file", URI: "file:///tmp/" + string(rune('a'+i)) + ".txt"}},
			ShardIndex: i,
			ShardCount: count,
		})
	}
	return children
}

func serviceRepoChildRequests(repoURI string) []types.ParallelChildRequest {
	return []types.ParallelChildRequest{
		{
			InputRefs:  []types.InputRef{{Type: "repo", URI: repoURI}},
			ShardKey:   "repo:src",
			ShardIndex: 0,
			ShardCount: 2,
		},
		{
			InputRefs:  []types.InputRef{{Type: "repo", URI: repoURI}},
			ShardKey:   "repo:tests",
			ShardIndex: 1,
			ShardCount: 2,
		},
	}
}

func loadJSONFileForTest(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json file %s: %v", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode json file %s: %v", path, err)
	}
	return payload
}

func makeFanoutJob(id, taskType, rootJobID string, state types.JobState, submittedAt time.Time, shardKey string, shardIndex, shardCount int) types.Job {
	return types.Job{
		ID:        id,
		TaskType:  taskType,
		State:     state,
		RootJobID: rootJobID,
		Orchestration: &types.OrchestrationInfo{
			RootJobID:  rootJobID,
			Strategy:   "fanout_child",
			ShardKey:   shardKey,
			ShardIndex: shardIndex,
			ShardCount: shardCount,
		},
		CreatedAt:   submittedAt,
		UpdatedAt:   submittedAt,
		SubmittedAt: submittedAt,
	}
}

func makeAggregatorJob(id, taskType, rootJobID, aggregationKey string, state types.JobState, submittedAt time.Time) types.Job {
	return types.Job{
		ID:        id,
		TaskType:  taskType,
		State:     state,
		RootJobID: rootJobID,
		Orchestration: &types.OrchestrationInfo{
			RootJobID:      rootJobID,
			Strategy:       "aggregator",
			AggregationKey: aggregationKey,
		},
		CreatedAt:   submittedAt,
		UpdatedAt:   submittedAt,
		SubmittedAt: submittedAt,
	}
}

func TestGetJobIngestsResultOnSuccess(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{
			status: backends.RunStatus{
				BackendRunID: "run-1",
				State:        types.JobStateSucceeded,
				RawState:     "COMPLETED",
				ExitCode:     "0:0",
			},
		},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:           "job_test",
		TaskType:     "document_summary",
		State:        types.JobStateQueued,
		BackendKind:  "fake",
		BackendRunID: "run-1",
		Request: types.SubmitJobRequest{
			TaskType:     "document_summary",
			OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	jobDir := filepath.Join(runRoot, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir job dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "result.json"), []byte(`{
  "schema_name": "document_summary_v1",
  "schema_version": "1.0.0",
  "payload": {
    "summary": "placeholder summary"
  }
}`), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "artifacts.json"), []byte(`[
  {
    "artifact_id": "artifact_1",
    "artifact_type": "result_blob",
    "path": "result.json"
  }
]`), 0o644); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}

	got, err := svc.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded, got %q", got.State)
	}
	if got.Result == nil || got.Result.SchemaName != "document_summary_v1" {
		t.Fatalf("expected ingested result, got %#v", got.Result)
	}
	if len(got.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(got.Artifacts))
	}
}

func TestSubmitJobStoresSubmittingActor(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	ctx := aliceUserCtx()
	resp, err := svc.SubmitJob(ctx, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	job, err := jobStore.GetJob(context.Background(), resp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.SubmittedBy != "alice" {
		t.Fatalf("expected submitted_by alice, got %q", job.SubmittedBy)
	}
}

func TestListJobsFiltersByActor(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		t.TempDir(),
		".",
	)

	now := time.Now().UTC()
	for _, job := range []types.Job{
		{ID: "job_alice", TaskType: "document_summary", State: types.JobStateQueued, SubmittedBy: "alice", CreatedAt: now, UpdatedAt: now, SubmittedAt: now},
		{ID: "job_bob", TaskType: "document_summary", State: types.JobStateQueued, SubmittedBy: "bob", CreatedAt: now, UpdatedAt: now, SubmittedAt: now.Add(time.Second)},
	} {
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	jobs, err := svc.ListJobs(aliceUserCtx())
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job_alice" {
		t.Fatalf("unexpected filtered jobs: %#v", jobs)
	}

	allJobs, err := svc.ListJobs(rootAdminCtx())
	if err != nil {
		t.Fatalf("list admin jobs: %v", err)
	}
	if len(allJobs) != 2 {
		t.Fatalf("expected 2 jobs for admin, got %d", len(allJobs))
	}
}

func TestGetJobForbiddenForDifferentActor(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		t.TempDir(),
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_owned",
		TaskType:    "document_summary",
		State:       types.JobStateQueued,
		SubmittedBy: "alice",
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, err := svc.GetJob(bobUserCtx(), job.ID); err == nil {
		t.Fatal("expected forbidden error")
	}
}

func TestRootScopedOperationsRequireAccessToEntireRoot(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	svc := New(jobStore, fakeBackend{}, log.New(io.Discard, "", 0), t.TempDir(), ".")

	now := time.Now().UTC()
	for _, job := range []types.Job{
		{
			ID: "job_alice_root", TaskType: "document_summary", State: types.JobStateQueued, RootJobID: "root_mixed",
			SubmittedBy: "alice",
			Request:     types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
			Orchestration: &types.OrchestrationInfo{
				RootJobID: "root_mixed", Strategy: "fanout_child", ShardIndex: 0, ShardCount: 2,
			},
			CreatedAt: now, UpdatedAt: now, SubmittedAt: now,
		},
		{
			ID: "job_bob_root", TaskType: "document_summary", State: types.JobStateDispatching, RootJobID: "root_mixed",
			SubmittedBy: "bob",
			Request:     types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
			Orchestration: &types.OrchestrationInfo{
				RootJobID: "root_mixed", Strategy: "fanout_child", ShardIndex: 1, ShardCount: 2,
			},
			CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second), SubmittedAt: now.Add(time.Second),
		},
	} {
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	if _, err := svc.GetRootJobStatus(aliceUserCtx(), "root_mixed"); err == nil {
		t.Fatal("expected forbidden root status")
	}
	if _, err := svc.RetryFailedRootShards(aliceUserCtx(), types.RetryFailedRootShardsRequest{RootJobID: "root_mixed"}); err == nil {
		t.Fatal("expected forbidden root retry")
	}
	if _, err := svc.ReleaseDeferredRootChunks(aliceUserCtx(), types.ReleaseDeferredRootChunksRequest{RootJobID: "root_mixed"}); err == nil {
		t.Fatal("expected forbidden root release")
	}
}

func TestSubmitJobWritesAuditEvent(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	auditLogger := audit.NewMemoryLogger()
	svc := NewWithAudit(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		auditLogger,
		runRoot,
		".",
	)

	ctx := aliceUserCtx()
	if _, err := svc.SubmitJob(ctx, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	}); err != nil {
		t.Fatalf("submit job: %v", err)
	}

	if len(auditLogger.Events) == 0 {
		t.Fatal("expected audit events")
	}
	event := auditLogger.Events[len(auditLogger.Events)-1]
	if event.Action != "job.submit" || event.Actor != "alice" || event.Outcome != "success" {
		t.Fatalf("unexpected audit event: %#v", event)
	}
}

func TestSubmitJobNormalizesOrchestrationMetadata(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	resp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "document_summary",
		Orchestration: types.OrchestrationRequest{
			ParentJobID:    "job_parent_01",
			ShardKey:       "repo:src",
			ShardIndex:     2,
			ShardCount:     8,
			AggregationKey: "repo-pass-1",
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	job, err := jobStore.GetJob(context.Background(), resp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.ParentJobID != "job_parent_01" {
		t.Fatalf("expected parent job id, got %q", job.ParentJobID)
	}
	if job.RootJobID != "job_parent_01" {
		t.Fatalf("expected root job id to default to parent, got %q", job.RootJobID)
	}
	if job.Orchestration == nil {
		t.Fatal("expected orchestration metadata")
	}
	if job.Orchestration.Strategy != "fanout_child" {
		t.Fatalf("expected fanout_child, got %q", job.Orchestration.Strategy)
	}
	if job.Orchestration.ShardIndex != 2 || job.Orchestration.ShardCount != 8 {
		t.Fatalf("unexpected shard metadata: %#v", job.Orchestration)
	}
}

func TestSubmitParallelJobsCreatesSharedRootChildren(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	resp, err := svc.SubmitParallelJobs(context.Background(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children: []types.ParallelChildRequest{
			{InputRefs: []types.InputRef{{Type: "file", URI: "file:///tmp/a.txt"}}, ShardKey: "repo:a", ShardIndex: 0, ShardCount: 2},
			{InputRefs: []types.InputRef{{Type: "file", URI: "file:///tmp/b.txt"}}, ShardKey: "repo:b", ShardIndex: 1, ShardCount: 2},
		},
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	if resp.RootJobID == "" {
		t.Fatal("expected root job id")
	}
	if resp.ChildCount != 2 || len(resp.Children) != 2 {
		t.Fatalf("unexpected child count: %#v", resp)
	}

	for _, child := range resp.Children {
		job, err := jobStore.GetJob(context.Background(), child.JobID)
		if err != nil {
			t.Fatalf("get child job: %v", err)
		}
		if job.RootJobID != resp.RootJobID {
			t.Fatalf("expected shared root job id %q, got %q", resp.RootJobID, job.RootJobID)
		}
		if job.Orchestration == nil || job.Orchestration.Strategy != "fanout_child" {
			t.Fatalf("expected fanout_child orchestration, got %#v", job.Orchestration)
		}
	}
}

func TestSubmitParallelJobsUsesBatchBackendForUncachedChildren(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	backend := &fakeBatchBackend{}
	svc := New(jobStore, backend, log.New(io.Discard, "", 0), runRoot, ".")

	resp, err := svc.SubmitParallelJobs(context.Background(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     serviceFileChildRequests(2),
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	if backend.batchCalls != 1 {
		t.Fatalf("expected 1 batch call, got %d", backend.batchCalls)
	}
	if len(backend.submittedJobs) != 2 {
		t.Fatalf("expected 2 submitted jobs, got %d", len(backend.submittedJobs))
	}
	for i, child := range resp.Children {
		job, err := jobStore.GetJob(context.Background(), child.JobID)
		if err != nil {
			t.Fatalf("get child job: %v", err)
		}
		if job.BackendRunID != "batch-run-0" && job.BackendRunID != "batch-run-1" {
			t.Fatalf("expected chunk-local batch run id, got %q for child %d", job.BackendRunID, i)
		}
	}
}

func TestSubmitParallelJobsChunksLargeBatchSubmission(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	backend := &fakeBatchBackend{}
	svc := NewWithAuditAndOptions(jobStore, backend, log.New(io.Discard, "", 0), audit.NewNopLogger(), runRoot, ".", Options{
		ParallelMaxBatchSize: 2,
	})

	children := serviceDocChildRequests(5)

	resp, err := svc.SubmitParallelJobs(context.Background(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     children,
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	if backend.batchCalls != 2 {
		t.Fatalf("expected 2 batch calls for chunked submission, got %d", backend.batchCalls)
	}
	if len(backend.batchSizes) != 2 || backend.batchSizes[0] != 2 || backend.batchSizes[1] != 2 {
		t.Fatalf("expected batch sizes [2 2], got %#v", backend.batchSizes)
	}
	if len(backend.submittedJobs) != 4 {
		t.Fatalf("expected 4 batched jobs, got %d", len(backend.submittedJobs))
	}
	if len(resp.Children) != 5 {
		t.Fatalf("expected 5 children, got %#v", resp)
	}
	lastJob, err := jobStore.GetJob(context.Background(), resp.Children[4].JobID)
	if err != nil {
		t.Fatalf("get last child job: %v", err)
	}
	if lastJob.BackendRunID != "single-run-1" {
		t.Fatalf("expected single-run fallback for final singleton chunk, got %q", lastJob.BackendRunID)
	}
}

func TestSubmitParallelJobsThrottlesActiveRootBatchesAndDeferredReducer(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	backend := &fakeBatchBackend{
		status: backends.RunStatus{State: types.JobStateQueued, RawState: "PENDING"},
	}
	svc := NewWithAuditAndOptions(jobStore, backend, log.New(io.Discard, "", 0), audit.NewNopLogger(), runRoot, ".", Options{
		ParallelMaxBatchSize:     2,
		ParallelMaxActiveBatches: 1,
	})

	children := make([]types.ParallelChildRequest, 0, 5)
	for i := 0; i < 5; i++ {
		children = append(children, types.ParallelChildRequest{
			InputRefs:  []types.InputRef{{Type: "file", URI: "file:///tmp/doc-" + string(rune('a'+i)) + ".txt"}},
			ShardIndex: i,
			ShardCount: 5,
		})
	}

	resp, err := svc.SubmitParallelJobs(context.Background(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     children,
		Reducer: &types.ParallelReducerRequest{
			TaskType:     "document_summary",
			OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		},
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	if backend.batchCalls != 1 || len(backend.batchSizes) != 1 || backend.batchSizes[0] != 2 {
		t.Fatalf("expected only first chunk to submit immediately, got calls=%d sizes=%#v", backend.batchCalls, backend.batchSizes)
	}
	if resp.ReducerJob == nil || resp.ReducerJob.State != types.JobStateDispatching {
		t.Fatalf("expected deferred reducer placeholder, got %#v", resp.ReducerJob)
	}
	if resp.Children[0].State != types.JobStateQueued || resp.Children[1].State != types.JobStateQueued {
		t.Fatalf("expected first chunk queued, got %#v", resp.Children[:2])
	}
	if resp.Children[2].State != types.JobStateDispatching || resp.Children[4].State != types.JobStateDispatching {
		t.Fatalf("expected later chunks dispatching, got %#v", resp.Children)
	}
	root0, err := svc.GetRootJobStatus(context.Background(), resp.RootJobID)
	if err != nil {
		t.Fatalf("get initial root status: %v", err)
	}
	if root0.DispatchingChildren != 3 || root0.PendingChildren != 3 || root0.ActiveChunks != 1 || root0.PendingChunks != 2 || !root0.ReducerDeferred {
		t.Fatalf("unexpected initial throttling metrics: %#v", root0)
	}

	backend.status = backends.RunStatus{State: types.JobStateSucceeded, RawState: "COMPLETED", ExitCode: "0:0"}
	if _, err := svc.GetRootJobStatus(context.Background(), resp.RootJobID); err != nil {
		t.Fatalf("get root status release 2nd chunk: %v", err)
	}
	if backend.batchCalls != 2 || len(backend.batchSizes) != 2 || backend.batchSizes[1] != 2 {
		t.Fatalf("expected second chunk release, got calls=%d sizes=%#v", backend.batchCalls, backend.batchSizes)
	}

	if _, err := svc.GetRootJobStatus(context.Background(), resp.RootJobID); err != nil {
		t.Fatalf("get root status release singleton chunk: %v", err)
	}
	lastJob, err := jobStore.GetJob(context.Background(), resp.Children[4].JobID)
	if err != nil {
		t.Fatalf("get last child job: %v", err)
	}
	if lastJob.BackendRunID != "single-run-1" || lastJob.State != types.JobStateQueued {
		t.Fatalf("expected singleton chunk dispatch after slots freed, got %#v", lastJob)
	}
	root2, err := svc.GetRootJobStatus(context.Background(), resp.RootJobID)
	if err != nil {
		t.Fatalf("get root status after singleton dispatch: %v", err)
	}
	if root2.PendingChildren != 0 || root2.ActiveChunks != 0 || root2.PendingChunks != 0 || root2.ReducerDeferred {
		t.Fatalf("unexpected post-dispatch throttling metrics: %#v", root2)
	}

	if _, err := svc.GetRootJobStatus(context.Background(), resp.RootJobID); err != nil {
		t.Fatalf("get root status release reducer: %v", err)
	}
	reducerJob, err := jobStore.GetJob(context.Background(), resp.ReducerJob.JobID)
	if err != nil {
		t.Fatalf("get reducer job: %v", err)
	}
	if reducerJob.BackendRunID == "" || reducerJob.State == types.JobStateDispatching {
		t.Fatalf("expected deferred reducer placeholder to submit in place, got %#v", reducerJob)
	}
	root3, err := svc.GetRootJobStatus(context.Background(), resp.RootJobID)
	if err != nil {
		t.Fatalf("get root status after reducer submit: %v", err)
	}
	if root3.ReducerDeferred {
		t.Fatalf("expected reducer_deferred=false after reducer submission, got %#v", root3)
	}
}

func TestReleaseDeferredRootChunksForcesAdditionalChunkRelease(t *testing.T) {
	svc, backend := newServiceThrottledBatchFixture(t, Options{
		ParallelMaxBatchSize:     2,
		ParallelMaxActiveBatches: 1,
	})

	children := make([]types.ParallelChildRequest, 0, 5)
	for i := 0; i < 5; i++ {
		children = append(children, types.ParallelChildRequest{
			InputRefs:  []types.InputRef{{Type: "file", URI: "file:///tmp/doc-" + string(rune('a'+i)) + ".txt"}},
			ShardIndex: i,
			ShardCount: 5,
		})
	}

	resp, err := svc.SubmitParallelJobs(context.Background(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     children,
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	if backend.batchCalls != 1 {
		t.Fatalf("expected initial throttled submission, got %d", backend.batchCalls)
	}

	release, err := svc.ReleaseDeferredRootChunks(context.Background(), types.ReleaseDeferredRootChunksRequest{
		RootJobID:            resp.RootJobID,
		MaxAdditionalBatches: 1,
	})
	if err != nil {
		t.Fatalf("release deferred root chunks: %v", err)
	}
	if release.ReleasedChunks != 1 || release.ReleasedChildren != 2 {
		t.Fatalf("expected one extra chunk release, got %#v", release)
	}
	if release.CumulativeForcedReleaseChunks != 1 || release.RemainingForcedReleaseBudget != 0 {
		t.Fatalf("expected direct forced-release counters, got %#v", release)
	}
	if backend.batchCalls != 2 || len(backend.batchSizes) != 2 || backend.batchSizes[1] != 2 {
		t.Fatalf("expected second batch submission via release action, got calls=%d sizes=%#v", backend.batchCalls, backend.batchSizes)
	}
	if release.RootStatus.PendingChunks != 1 || release.RootStatus.ActiveChunks != 2 {
		t.Fatalf("expected updated root throttling metrics after forced release, got %#v", release.RootStatus)
	}
}

func TestReleaseDeferredRootChunksRejectsNonAdminRequestAboveCap(t *testing.T) {
	svc, _ := newServiceThrottledBatchFixture(t, Options{
		ParallelMaxBatchSize:           2,
		ParallelMaxActiveBatches:       1,
		RootActionMaxAdditionalBatches: 1,
	})

	resp, err := svc.SubmitParallelJobs(aliceUserCtx(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     serviceFileChildRequests(4),
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}

	_, err = svc.ReleaseDeferredRootChunks(aliceUserCtx(), types.ReleaseDeferredRootChunksRequest{
		RootJobID:            resp.RootJobID,
		MaxAdditionalBatches: 2,
	})
	if err == nil {
		t.Fatal("expected forbidden release over cap")
	}
}

func TestReleaseDeferredRootChunksAllowsAdminRequestAboveCap(t *testing.T) {
	svc, _ := newServiceThrottledBatchFixture(t, Options{
		ParallelMaxBatchSize:           2,
		ParallelMaxActiveBatches:       1,
		RootActionMaxAdditionalBatches: 1,
	})

	resp, err := svc.SubmitParallelJobs(adminCtx(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     serviceFileChildRequests(4),
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}

	release, err := svc.ReleaseDeferredRootChunks(adminCtx(), types.ReleaseDeferredRootChunksRequest{
		RootJobID:            resp.RootJobID,
		MaxAdditionalBatches: 2,
	})
	if err != nil {
		t.Fatalf("admin release deferred root chunks: %v", err)
	}
	if release.ReleasedChunks < 1 {
		t.Fatalf("expected admin release to succeed, got %#v", release)
	}
}

func TestReleaseDeferredRootChunksRejectsCumulativeNonAdminEscalation(t *testing.T) {
	svc, _ := newServiceThrottledBatchFixture(t, Options{
		ParallelMaxBatchSize:           2,
		ParallelMaxActiveBatches:       1,
		RootActionMaxAdditionalBatches: 2,
	})

	resp, err := svc.SubmitParallelJobs(aliceUserCtx(), types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     serviceFileChildRequests(8),
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}

	for range 2 {
		if _, err := svc.ReleaseDeferredRootChunks(aliceUserCtx(), types.ReleaseDeferredRootChunksRequest{
			RootJobID:            resp.RootJobID,
			MaxAdditionalBatches: 1,
		}); err != nil {
			t.Fatalf("expected cumulative release within cap: %v", err)
		}
	}
	if _, err := svc.ReleaseDeferredRootChunks(aliceUserCtx(), types.ReleaseDeferredRootChunksRequest{
		RootJobID:            resp.RootJobID,
		MaxAdditionalBatches: 1,
	}); err == nil {
		t.Fatal("expected cumulative forced release over cap to be forbidden")
	}
}

func TestRetryFailedRootShardsRejectsNonAdminRequestAboveCap(t *testing.T) {
	svc, jobStore := newServiceRetryBudgetFixture(t, 1)
	seedFailedRetryRootJobs(t, jobStore, "root_retry_cap", "alice", 2)

	_, err := svc.RetryFailedRootShards(aliceUserCtx(), types.RetryFailedRootShardsRequest{
		RootJobID: "root_retry_cap",
	})
	if err == nil {
		t.Fatal("expected forbidden retry over cap")
	}
}

func TestRetryFailedRootShardsAllowsAdminRequestAboveCap(t *testing.T) {
	svc, jobStore := newServiceRetryBudgetFixture(t, 1)
	seedFailedRetryRootJobs(t, jobStore, "root_retry_admin", "admin", 2)

	resp, err := svc.RetryFailedRootShards(adminCtx(), types.RetryFailedRootShardsRequest{
		RootJobID: "root_retry_admin",
	})
	if err != nil {
		t.Fatalf("admin retry failed root shards: %v", err)
	}
	if resp.RetriedCount != 2 {
		t.Fatalf("expected admin retry to succeed for both shards, got %#v", resp)
	}
}

func TestRetryFailedRootShardsRejectsCumulativeNonAdminEscalation(t *testing.T) {
	svc, jobStore := newServiceRetryBudgetFixture(t, 1)
	seedFailedRetryRootJobs(t, jobStore, "root_retry_cumulative", "alice", 1)

	userCtx := aliceUserCtx()
	first, err := svc.RetryFailedRootShards(userCtx, types.RetryFailedRootShardsRequest{RootJobID: "root_retry_cumulative"})
	if err != nil {
		t.Fatalf("first retry failed root shards: %v", err)
	}
	if first.RetriedCount != 1 {
		t.Fatalf("expected one retried shard, got %#v", first)
	}
	if first.CumulativeRetriedShards != 1 || first.RemainingRetriedShardBudget != 0 {
		t.Fatalf("expected direct retry counters, got %#v", first)
	}

	retriedJob, err := jobStore.GetJob(context.Background(), first.RetriedShards[0].JobID)
	if err != nil {
		t.Fatalf("get retried job: %v", err)
	}
	retriedJob.State = types.JobStateFailed
	retriedJob.BackendRunID = ""
	if err := jobStore.UpdateJob(context.Background(), retriedJob); err != nil {
		t.Fatalf("mark retried job failed again: %v", err)
	}

	if _, err := svc.RetryFailedRootShards(userCtx, types.RetryFailedRootShardsRequest{RootJobID: "root_retry_cumulative"}); err == nil {
		t.Fatal("expected cumulative retry over cap to be forbidden")
	}
}

func TestSubmitParallelJobsWithReducerCompletesLocally(t *testing.T) {
	runRoot, err := os.MkdirTemp("", "broker-runroot-*")
	if err != nil {
		t.Fatalf("mkdir run root: %v", err)
	}
	t.Cleanup(func() { removeAllRetry(runRoot) })
	repoRoot, err := os.MkdirTemp("", "broker-repo-*")
	if err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	t.Cleanup(func() { removeAllRetry(repoRoot) })
	if err := os.MkdirAll(filepath.Join(repoRoot, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "tests"), 0o755); err != nil {
		t.Fatalf("mkdir tests: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "src", "main.py"), []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatalf("write src file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "tests", "test_main.py"), []byte("def test_ok():\n    assert True\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	projectRoot := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	jobStore := store.NewMemoryJobStore()
	backend := localbackend.NewBackend(config.Config{
		LocalMode:       "command",
		LocalScriptPath: filepath.Join(projectRoot, "deploy", "local", "broker_worker.sh"),
		RunRootPath:     runRoot,
		RepoRootPath:    projectRoot,
	})
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		projectRoot,
	)

	resp, err := svc.SubmitParallelJobs(context.Background(), types.SubmitParallelJobsRequest{
		TaskType:     "repo_summary",
		OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
		Children:     serviceRepoChildRequests("file://" + repoRoot),
		Reducer: &types.ParallelReducerRequest{
			TaskType:     "repo_summary",
			OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
			TaskParams: map[string]any{
				"aggregate_wait_seconds": 15,
			},
		},
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	if resp.ReducerJob == nil {
		t.Fatal("expected reducer job")
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		job, err := svc.GetJob(context.Background(), resp.ReducerJob.JobID)
		if err != nil {
			t.Fatalf("get reducer job: %v", err)
		}
		if job.State == types.JobStateSucceeded {
			if job.Result == nil {
				t.Fatal("expected reducer result")
			}
			payload := job.Result.Payload
			metrics, _ := payload["aggregate_metrics"].(map[string]any)
			if metrics == nil {
				t.Fatalf("expected aggregate_metrics, got %#v", payload)
			}
			return
		}
		if job.State == types.JobStateFailed || job.State == types.JobStateCancelled || job.State == types.JobStateTimedOut {
			t.Fatalf("reducer ended unexpectedly: state=%s error=%s", job.State, job.ResultError)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for reducer completion")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestGetRootJobStatusAggregatesChildrenAndReducer(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		t.TempDir(),
		".",
	)

	now := time.Now().UTC()
	for _, job := range []types.Job{
		makeFanoutJob("job_child_1", "repo_summary", "root_1", types.JobStateSucceeded, now, "", 0, 2),
		makeFanoutJob("job_child_2", "repo_summary", "root_1", types.JobStateSucceeded, now, "", 1, 2),
		func() types.Job {
			job := makeAggregatorJob("job_reduce", "repo_summary", "root_1", "repo-pass-1", types.JobStateRunning, now)
			job.Result = &types.Result{
				SchemaName:    "repo_summary_v1",
				SchemaVersion: "1.0.0",
				Payload: map[string]any{
					"summary": "agg",
					"aggregate_metrics": map[string]any{
						"children_total":     2,
						"children_succeeded": 2,
						"children_failed":    0,
						"coverage_fraction":  1.0,
					},
				},
			}
			return job
		}(),
	} {
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	status, err := svc.GetRootJobStatus(context.Background(), "root_1")
	if err != nil {
		t.Fatalf("get root status: %v", err)
	}
	if status.TotalJobs != 3 || status.ReducerJobID != "job_reduce" {
		t.Fatalf("unexpected root status: %#v", status)
	}
	if status.State != types.JobStateRunning {
		t.Fatalf("expected running root state, got %q", status.State)
	}
	if len(status.ChildJobIDs) != 2 {
		t.Fatalf("expected 2 child job ids, got %#v", status.ChildJobIDs)
	}
	if status.ChildrenTotal != 2 || status.ChildrenSucceeded != 2 || status.CoverageFraction != 1.0 {
		t.Fatalf("expected reducer metrics on root status, got %#v", status)
	}
	if status.DispatchingChildren != 0 || status.PendingChildren != 0 || status.ActiveChunks != 0 || status.PendingChunks != 0 || status.ReducerDeferred {
		t.Fatalf("expected zero dispatch throttling metrics, got %#v", status)
	}
}

func TestGetRootJobStatusUsesEffectiveLatestShardAttempts(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	svc := New(jobStore, fakeBackend{}, log.New(io.Discard, "", 0), t.TempDir(), ".")

	now := time.Now().UTC()
	jobs := []types.Job{
		makeFanoutJob("job_child_failed", "repo_summary", "root_retry", types.JobStateFailed, now.Add(-2*time.Minute), "repo:src", 0, 2),
		makeFanoutJob("job_child_retry", "repo_summary", "root_retry", types.JobStateSucceeded, now.Add(-1*time.Minute), "repo:src", 0, 2),
		makeFanoutJob("job_child_2", "repo_summary", "root_retry", types.JobStateSucceeded, now, "repo:test", 1, 2),
		func() types.Job {
			job := makeAggregatorJob("job_reduce", "repo_summary", "root_retry", "repo-pass-1", types.JobStateSucceeded, now.Add(30*time.Second))
			job.Result = &types.Result{
				SchemaName: "repo_summary_v1", SchemaVersion: "1.0.0",
				Payload: map[string]any{"aggregate_metrics": map[string]any{
					"children_total": 2, "children_succeeded": 2, "children_failed": 0, "coverage_fraction": 1.0,
				}},
			}
			return job
		}(),
	}
	for _, job := range jobs {
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	status, err := svc.GetRootJobStatus(context.Background(), "root_retry")
	if err != nil {
		t.Fatalf("get root status: %v", err)
	}
	if status.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded root state, got %#v", status)
	}
	if status.FailedJobs != 0 || status.SucceededJobs != 3 {
		t.Fatalf("expected only effective succeeded jobs to count, got %#v", status)
	}
	if len(status.ChildJobIDs) != 2 {
		t.Fatalf("expected 2 effective child jobs, got %#v", status.ChildJobIDs)
	}
}

func TestRetryFailedRootShardsRetriesOnlyFailedLatestChildrenAndResubmitsReducer(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(jobStore, fakeBackend{}, log.New(io.Discard, "", 0), runRoot, ".")

	now := time.Now().UTC()
	jobs := []types.Job{
		func() types.Job {
			job := makeFanoutJob("job_child_ok", "document_summary", "root_retry_2", types.JobStateSucceeded, now.Add(-2*time.Minute), "doc:a", 0, 2)
			job.Request = types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}}
			return job
		}(),
		func() types.Job {
			job := makeFanoutJob("job_child_failed", "document_summary", "root_retry_2", types.JobStateFailed, now.Add(-1*time.Minute), "doc:b", 1, 2)
			job.Request = types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}}
			return job
		}(),
		func() types.Job {
			job := makeAggregatorJob("job_reduce_failed", "document_summary", "root_retry_2", "aggregate", types.JobStateFailed, now)
			job.Request = types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}}
			return job
		}(),
	}
	for _, job := range jobs {
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	resp, err := svc.RetryFailedRootShards(context.Background(), types.RetryFailedRootShardsRequest{
		RootJobID:       "root_retry_2",
		ResubmitReducer: true,
	})
	if err != nil {
		t.Fatalf("retry failed root shards: %v", err)
	}
	if resp.RetriedCount != 1 || len(resp.RetriedShards) != 1 {
		t.Fatalf("expected one retried shard, got %#v", resp)
	}
	if resp.RetriedShards[0].PreviousJobID != "job_child_failed" {
		t.Fatalf("expected failed shard to be retried, got %#v", resp.RetriedShards)
	}
	if resp.ReducerJob == nil {
		t.Fatalf("expected reducer resubmission, got %#v", resp)
	}
	if resp.SkippedCount != 1 || len(resp.SkippedShards) != 1 || resp.SkippedShards[0].Reason != "already_succeeded" {
		t.Fatalf("expected succeeded shard skip, got %#v", resp)
	}

	root, err := svc.GetRootJobStatus(context.Background(), "root_retry_2")
	if err != nil {
		t.Fatalf("get root status: %v", err)
	}
	if root.State != types.JobStateQueued {
		t.Fatalf("expected queued root state after retry/resubmission, got %#v", root)
	}
	if root.FailedJobs != 0 {
		t.Fatalf("expected stale failed attempts to be excluded from effective root state, got %#v", root)
	}
}

func TestSubmitParallelLogAnalysisWithReducerCompletesLocally(t *testing.T) {
	runRoot, err := os.MkdirTemp("", "broker-log-runroot-*")
	if err != nil {
		t.Fatalf("mkdir run root: %v", err)
	}
	t.Cleanup(func() { removeAllRetry(runRoot) })
	repoRoot, err := os.MkdirTemp("", "broker-log-repo-*")
	if err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	t.Cleanup(func() { removeAllRetry(repoRoot) })

	logA := filepath.Join(repoRoot, "a.log")
	if err := os.WriteFile(logA, []byte("fatal error: generated header missing\n"), 0o644); err != nil {
		t.Fatalf("write logA: %v", err)
	}
	logBMissing := filepath.Join(repoRoot, "missing.log")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	projectRoot := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	jobStore := store.NewMemoryJobStore()
	backend := localbackend.NewBackend(config.Config{
		LocalMode:       "command",
		LocalScriptPath: filepath.Join(projectRoot, "deploy", "local", "broker_worker.sh"),
		RunRootPath:     runRoot,
		RepoRootPath:    projectRoot,
	})
	svc := New(jobStore, backend, log.New(io.Discard, "", 0), runRoot, projectRoot)

	resp, err := svc.SubmitParallelJobs(context.Background(), types.SubmitParallelJobsRequest{
		TaskType:     "log_analysis",
		OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
		Children: []types.ParallelChildRequest{
			{InputRefs: []types.InputRef{{Type: "file", URI: "file://" + logA}}, ShardIndex: 0, ShardCount: 2},
			{InputRefs: []types.InputRef{{Type: "file", URI: "file://" + logBMissing}}, ShardIndex: 1, ShardCount: 2},
		},
		Reducer: &types.ParallelReducerRequest{
			TaskType:     "log_analysis",
			OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
			TaskParams: map[string]any{
				"aggregate_wait_seconds": 15,
			},
		},
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	if resp.ReducerJob == nil {
		t.Fatal("expected reducer job")
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		job, err := svc.GetJob(context.Background(), resp.ReducerJob.JobID)
		if err != nil {
			t.Fatalf("get reducer job: %v", err)
		}
		if job.State == types.JobStateSucceeded {
			if job.Result == nil {
				t.Fatal("expected reducer result")
			}
			findings, _ := job.Result.Payload["top_findings"].([]any)
			if len(findings) == 0 {
				t.Fatalf("expected merged findings, got %#v", job.Result.Payload)
			}
			root, err := svc.GetRootJobStatus(context.Background(), resp.RootJobID)
			if err != nil {
				t.Fatalf("get root status: %v", err)
			}
			if root.CoverageFraction >= 1.0 || root.ChildrenSucceeded != 1 || root.ChildrenFailed != 1 {
				t.Fatalf("unexpected root metrics: %#v", root)
			}
			warnings, _ := job.Result.Payload["warnings"].([]any)
			if len(warnings) == 0 {
				t.Fatalf("expected partial-reduce warning, got %#v", job.Result.Payload)
			}
			return
		}
		if job.State == types.JobStateFailed || job.State == types.JobStateCancelled || job.State == types.JobStateTimedOut {
			t.Fatalf("reducer ended unexpectedly: state=%s error=%s", job.State, job.ResultError)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for reducer completion")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func removeAllRetry(path string) {
	for range 20 {
		if err := os.RemoveAll(path); err == nil || errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.RemoveAll(path)
}

func TestSubmitJobRunsToCompletionWithLocalBackend(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	backend := localbackend.NewBackend(config.Config{
		LocalMode:       "command",
		LocalScriptPath: filepath.Join(repoRoot, "deploy", "local", "broker_worker.sh"),
		RunRootPath:     runRoot,
		RepoRootPath:    repoRoot,
	})
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	inputPath := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(inputPath, []byte("Local backend validation document.\nThis should complete through the real worker.\n"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	resp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + inputPath},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		job, err := svc.GetJob(context.Background(), resp.JobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.State == types.JobStateSucceeded {
			if job.Result == nil || job.Result.SchemaName != "document_summary_v1" {
				t.Fatalf("expected document_summary_v1 result, got %#v", job.Result)
			}
			return
		}
		if job.State == types.JobStateFailed || job.State == types.JobStateCancelled || job.State == types.JobStateTimedOut {
			t.Fatalf("job ended unexpectedly: state=%s error=%s", job.State, job.ResultError)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for local backend job completion")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestForbiddenGetJobWritesAuditEvent(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	auditLogger := audit.NewMemoryLogger()
	svc := NewWithAudit(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		auditLogger,
		t.TempDir(),
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_owned",
		TaskType:    "document_summary",
		State:       types.JobStateQueued,
		SubmittedBy: "alice",
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, err := svc.GetJob(bobUserCtx(), job.ID); err == nil {
		t.Fatal("expected forbidden error")
	}

	if len(auditLogger.Events) == 0 {
		t.Fatal("expected audit events")
	}
	event := auditLogger.Events[len(auditLogger.Events)-1]
	if event.Action != "job.get_status" || event.Outcome != "forbidden" || event.JobID != job.ID {
		t.Fatalf("unexpected audit event: %#v", event)
	}
}

func TestSubmitJobStagesExecutionBundle(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		"/repo/root",
	)

	resp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file:///tmp/example.txt"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, resp.JobID)
	if _, err := os.Stat(filepath.Join(jobDir, "job_spec.json")); err != nil {
		t.Fatalf("expected job spec: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "execution_plan.json")); err != nil {
		t.Fatalf("expected execution plan: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "input_manifest.json")); err != nil {
		t.Fatalf("expected input manifest: %v", err)
	}
}

func TestSubmitJobAppliesTierModelDefaultsToExecutionPlan(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	cfg := config.Config{
		ModelProfileP40:               "gpt-oss-20b.p40",
		ModelProfileA100:              "qwen3-coder-30b.a100",
		RuntimeLlamaCPPBaseURL:        "http://127.0.0.1:8088",
		RuntimeLlamaCPPTimeoutSeconds: 17,
	}
	svc := NewWithAuditAndOptionsAndConfig(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		audit.NewNopLogger(),
		runRoot,
		"/repo/root",
		Options{},
		&cfg,
	)

	resp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "rag_compress",
		InputRefs: []types.InputRef{
			{Type: "repo", URI: "file:///tmp/example-repo"},
		},
		ExecutionProfile: types.ExecutionProfile{
			Tier: "p40-rag-compression",
		},
		OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, resp.JobID)
	plan := loadJSONFileForTest(t, filepath.Join(jobDir, "execution_plan.json"))
	if plan["selected_model"] != "gpt-oss-20b.p40" {
		t.Fatalf("expected selected_model gpt-oss-20b.p40, got %#v", plan["selected_model"])
	}
	if plan["runtime_backend"] != "llama.cpp" {
		t.Fatalf("expected runtime_backend llama.cpp, got %#v", plan["runtime_backend"])
	}
	runtimeConnection, _ := plan["runtime_connection"].(map[string]any)
	if runtimeConnection["base_url"] != "http://127.0.0.1:8088" {
		t.Fatalf("expected runtime_connection.base_url, got %#v", runtimeConnection)
	}
	if runtimeConnection["timeout_seconds"] != float64(17) {
		t.Fatalf("expected runtime_connection.timeout_seconds 17, got %#v", runtimeConnection)
	}
}

func TestGetJobFailsOnInvalidResultSchema(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{
			status: backends.RunStatus{
				BackendRunID: "run-1",
				State:        types.JobStateSucceeded,
				RawState:     "COMPLETED",
				ExitCode:     "0:0",
			},
		},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:           "job_invalid",
		TaskType:     "document_summary",
		State:        types.JobStateQueued,
		BackendKind:  "fake",
		BackendRunID: "run-1",
		Request: types.SubmitJobRequest{
			TaskType:     "document_summary",
			OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	jobDir := filepath.Join(runRoot, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir job dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "result.json"), []byte(`{
  "schema_name": "placeholder_v1",
  "schema_version": "1.0.0",
  "payload": {
    "summary": "placeholder summary"
  }
}`), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}

	got, err := svc.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != types.JobStateFailed {
		t.Fatalf("expected failed state, got %q", got.State)
	}
	if got.ResultError == "" {
		t.Fatal("expected result error")
	}
}

func TestGetJobRefreshesProgressFromHeartbeat(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{
			status: backends.RunStatus{
				BackendRunID: "run-1",
				State:        types.JobStateRunning,
				RawState:     "RUNNING",
				ExitCode:     "0:0",
			},
		},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:           "job_progress",
		TaskType:     "document_summary",
		State:        types.JobStateQueued,
		BackendKind:  "fake",
		BackendRunID: "run-1",
		Request: types.SubmitJobRequest{
			TaskType:     "document_summary",
			OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	jobDir := filepath.Join(runRoot, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir job dir: %v", err)
	}
	heartbeat := `{
  "job_id": "job_progress",
  "state": "running",
  "phase": "preprocessing",
  "percent": 35,
  "message": "Loading source document",
  "timestamp": "2026-06-26T12:00:00Z",
  "metrics": {
    "documents_processed": 1
  }
}`
	if err := os.WriteFile(filepath.Join(jobDir, "heartbeat.json"), []byte(heartbeat), 0o644); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	got, err := svc.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != types.JobStateRunning {
		t.Fatalf("expected running, got %q", got.State)
	}
	if got.Progress == nil {
		t.Fatal("expected progress info")
	}
	if got.Progress.Phase != "preprocessing" || got.Progress.Percent != 35 {
		t.Fatalf("unexpected progress: %#v", got.Progress)
	}
	if got.Progress.Metrics["documents_processed"] != float64(1) {
		t.Fatalf("unexpected metrics: %#v", got.Progress.Metrics)
	}
}

func TestGetJobAppliesBrokerRetrievalPolicyWarnings(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{
			status: backends.RunStatus{
				BackendRunID: "run-1",
				State:        types.JobStateSucceeded,
				RawState:     "COMPLETED",
				ExitCode:     "0:0",
			},
		},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:           "job_policy_result",
		TaskType:     "rag_compress",
		State:        types.JobStateQueued,
		BackendKind:  "fake",
		BackendRunID: "run-1",
		Request: types.SubmitJobRequest{
			TaskType:     "rag_compress",
			OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
			ExecutionProfile: types.ExecutionProfile{
				Backend: "slurm",
				Tier:    "p40-rag-compression",
				Runtime: "llama.cpp",
			},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	jobDir := filepath.Join(runRoot, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir job dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "result.json"), []byte(`{
  "schema_name": "rag_evidence_pack_v1",
  "schema_version": "1.0.0",
  "payload": {
    "query": "why did the build fail?",
    "retrieval": {
      "strategies": ["ripgrep"],
      "chunks_considered": 1,
      "chunks_indexed": 1,
      "chunks_retrieved": 1,
      "chunks_reranked": 1,
      "chunks_deduplicated": 0,
      "chunks_compressed": 1,
      "requested_strategies": ["ripgrep"],
      "skipped_strategies": [],
      "strategy_hits": {},
      "strategy_stats": [{"strategy":"ripgrep","backend_mode":"fallback"}]
    },
    "retrieval_plan": {
      "requested_strategies": ["ripgrep"],
      "effective_strategies": ["ripgrep"]
    },
    "retrieval_trace": {
      "strategy_executions": [
        {"strategy":"ripgrep","backend_mode":"fallback","backend_detail":"deterministic_path_scan"}
      ]
    },
    "policy_signals": {
      "mode_counts": {"fallback": 1},
      "degraded_strategies": [{"strategy":"ripgrep","backend_mode":"fallback"}],
      "real_backend_required_recommended": true,
      "warnings": ["LOCAL_RETRIEVAL_DEGRADED", "NO_REAL_RETRIEVAL_BACKEND"]
    },
    "evidence": [{"id":"ev_001"}],
    "budget": {"retrieved_chunk_tokens": 10}
  }
}`), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "artifacts.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}

	got, err := svc.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Result == nil {
		t.Fatal("expected result")
	}
	warnings, _ := got.Result.Payload["warnings"].([]any)
	if !containsAnyString(warnings, []string{"broker_local_retrieval_degraded", "broker_no_real_retrieval_backend"}) {
		t.Fatalf("expected broker retrieval policy warnings, got %#v", got.Result.Payload)
	}
	if got.ResultError != "broker_policy_no_real_retrieval_backend" {
		t.Fatalf("expected broker policy result error, got %#v", got.ResultError)
	}
	retryRecommendation, _ := got.Result.Payload["broker_retry_recommendation"].(map[string]any)
	if retryRecommendation["recommended"] != true {
		t.Fatalf("expected broker retry recommendation, got %#v", got.Result.Payload)
	}
	executionProfile, _ := retryRecommendation["execution_profile"].(map[string]any)
	if executionProfile["tier"] != "a100-reasoning" {
		t.Fatalf("expected escalated retry tier, got %#v", retryRecommendation)
	}
	placementHint, _ := retryRecommendation["placement_hint"].(map[string]any)
	if placementHint["tier_preference"] != "a100-reasoning" || placementHint["preemptible"] != true {
		t.Fatalf("expected placement hint in retry recommendation, got %#v", retryRecommendation)
	}
}

func TestGetJobLogsRedactsAndTruncates(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_logs",
		TaskType: "log_analysis",
		State:    types.JobStateRunning,
		Request: types.SubmitJobRequest{
			TaskType:     "log_analysis",
			OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	jobDir := filepath.Join(runRoot, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir job dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "stdout.log"), []byte("token=abc123\nhello stdout\n"), 0o644); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "stderr.log"), []byte("Bearer secret-token-value\nline two\n"), 0o644); err != nil {
		t.Fatalf("write stderr: %v", err)
	}

	logs, err := svc.GetJobLogs(context.Background(), job.ID, "combined", 40)
	if err != nil {
		t.Fatalf("get job logs: %v", err)
	}
	if !logs.Truncated {
		t.Fatal("expected truncated logs")
	}
	if logs.Stream != "combined" {
		t.Fatalf("unexpected stream %q", logs.Stream)
	}
	if len(logs.SourceRefs) != 2 {
		t.Fatalf("expected 2 source refs, got %d", len(logs.SourceRefs))
	}
	if containsAny(logs.Content, []string{"abc123", "secret-token-value"}) {
		t.Fatalf("expected redacted secrets, got %q", logs.Content)
	}
}

func TestGetJobLogsDeniedForSensitiveClassification(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_sensitive_logs",
		TaskType: "log_analysis",
		State:    types.JobStateRunning,
		Request: types.SubmitJobRequest{
			TaskType: "log_analysis",
			InputRefs: []types.InputRef{
				{Type: "file", URI: "file:///tmp/secret.log", Classification: "phi"},
			},
			OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	_, err := svc.GetJobLogs(context.Background(), job.ID, "combined", 1024)
	if err == nil {
		t.Fatal("expected policy denial")
	}
	if !strings.Contains(err.Error(), "policy denied") {
		t.Fatalf("expected policy denial error, got %v", err)
	}
}

func TestGetJobLogsOverrideAllowsSensitiveClassification(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_sensitive_override",
		TaskType: "log_analysis",
		State:    types.JobStateRunning,
		Request: types.SubmitJobRequest{
			TaskType: "log_analysis",
			InputRefs: []types.InputRef{
				{Type: "file", URI: "file:///tmp/secret.log", Classification: "restricted"},
			},
			TaskParams:   map[string]any{"allow_log_release": true},
			OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	jobDir := filepath.Join(runRoot, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir job dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "stdout.log"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write stdout: %v", err)
	}

	logs, err := svc.GetJobLogs(context.Background(), job.ID, "stdout", 1024)
	if err != nil {
		t.Fatalf("expected override to allow logs: %v", err)
	}
	if logs.Content == "" {
		t.Fatal("expected log content")
	}
}

func TestGetReleasedResultRedactsSensitiveFieldsAndWithholdsArtifacts(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_sensitive_result",
		TaskType: "repo_summary",
		State:    types.JobStateSucceeded,
		Request: types.SubmitJobRequest{
			TaskType: "repo_summary",
			InputRefs: []types.InputRef{
				{Type: "directory", URI: "file:///tmp/repo", Classification: "restricted"},
			},
			OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
		},
		Result: &types.Result{
			SchemaName:    "repo_summary_v1",
			SchemaVersion: "1.0.0",
			Payload: map[string]any{
				"summary": "summary",
				"entrypoints": []any{
					map[string]any{"path": "broker/cmd/main.go", "kind": "service_entrypoint"},
				},
				"warnings": []any{"worker_warning"},
			},
		},
		Artifacts: []types.Artifact{
			{ArtifactID: "artifact_1", ArtifactType: "chunk_manifest", Path: "/tmp/manifest.json"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	release, err := svc.GetReleasedResult(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get released result: %v", err)
	}
	if release.Result == nil {
		t.Fatal("expected result")
	}
	entrypoints := release.Result.Payload["entrypoints"].([]any)
	first := entrypoints[0].(map[string]any)
	if first["path"] != "[REDACTED]" {
		t.Fatalf("expected redacted path, got %#v", first["path"])
	}
	if len(release.Artifacts) != 0 {
		t.Fatalf("expected artifacts withheld, got %#v", release.Artifacts)
	}
	warnings := release.Result.Payload["warnings"].([]any)
	if !containsAnyString(warnings, []string{"broker_redacted_sensitive_fields", "broker_withheld_artifacts"}) {
		t.Fatalf("expected broker warnings, got %#v", warnings)
	}
}

func TestGetReleasedResultAllowsArtifactsWithOverrideButStripsPaths(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_sensitive_artifacts",
		TaskType: "log_analysis",
		State:    types.JobStateSucceeded,
		Request: types.SubmitJobRequest{
			TaskType: "log_analysis",
			InputRefs: []types.InputRef{
				{Type: "file", URI: "file:///tmp/build.log", Classification: "phi"},
			},
			TaskParams:   map[string]any{"allow_artifact_release": true},
			OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
		},
		Result: &types.Result{
			SchemaName:    "log_analysis_v1",
			SchemaVersion: "1.0.0",
			Payload:       map[string]any{"summary": "summary"},
		},
		Artifacts: []types.Artifact{
			{ArtifactID: "artifact_1", ArtifactType: "redacted_excerpt", Path: "/tmp/excerpt.txt"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	release, err := svc.GetReleasedResult(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get released result: %v", err)
	}
	if len(release.Artifacts) != 1 {
		t.Fatalf("expected released artifacts, got %#v", release.Artifacts)
	}
	if release.Artifacts[0].Path != "" {
		t.Fatalf("expected stripped artifact path, got %#v", release.Artifacts[0].Path)
	}
}

func TestSubmitAndIngestDocumentSummaryWorker(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	runRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	inputPath := filepath.Join(runRoot, "source.txt")
	if err := os.WriteFile(inputPath, []byte("Worker integration test.\n- Point one\n- Point two\n"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	backend := &mutableFakeBackend{}
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	submitResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + inputPath, ContentHash: "sha256:test"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	cmd := exec.Command(
		"python3",
		filepath.Join(repoRoot, "workers", "document-summary", "main.py"),
		"--job-spec", filepath.Join(jobDir, "job_spec.json"),
		"--input-manifest", filepath.Join(jobDir, "input_manifest.json"),
		"--output-dir", jobDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run document worker: %v: %s", err, string(output))
	}

	backend.status = backends.RunStatus{
		BackendRunID: "run-1",
		State:        types.JobStateSucceeded,
		RawState:     "COMPLETED",
		ExitCode:     "0:0",
	}

	got, err := svc.GetJob(context.Background(), submitResp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded, got %q", got.State)
	}
	if got.Result == nil || got.Result.SchemaName != "document_summary_v1" {
		t.Fatalf("expected document_summary_v1 result, got %#v", got.Result)
	}
	if len(got.Artifacts) != 1 {
		t.Fatalf("expected one artifact, got %d", len(got.Artifacts))
	}
	if got.Progress == nil || got.Progress.State != "completed" || got.Progress.Percent != 100 {
		t.Fatalf("expected completed progress, got %#v", got.Progress)
	}
}

func TestSubmitAndIngestLogAnalysisWorker(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	runRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	inputPath := filepath.Join(runRoot, "build.log")
	logText := "2026-06-26T12:01:00Z build started\nfatal error: generated/config.h: No such file or directory\n"
	if err := os.WriteFile(inputPath, []byte(logText), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	backend := &mutableFakeBackend{}
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	submitResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "log_analysis",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + inputPath, ContentHash: "sha256:test"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	cmd := exec.Command(
		"python3",
		filepath.Join(repoRoot, "workers", "log-analysis", "main.py"),
		"--job-spec", filepath.Join(jobDir, "job_spec.json"),
		"--input-manifest", filepath.Join(jobDir, "input_manifest.json"),
		"--output-dir", jobDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run log worker: %v: %s", err, string(output))
	}

	backend.status = backends.RunStatus{
		BackendRunID: "run-1",
		State:        types.JobStateSucceeded,
		RawState:     "COMPLETED",
		ExitCode:     "0:0",
	}

	got, err := svc.GetJob(context.Background(), submitResp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded, got %q", got.State)
	}
	if got.Result == nil || got.Result.SchemaName != "log_analysis_v1" {
		t.Fatalf("expected log_analysis_v1 result, got %#v", got.Result)
	}
	if len(got.Artifacts) != 1 {
		t.Fatalf("expected one artifact, got %d", len(got.Artifacts))
	}
	if got.Progress == nil || got.Progress.State != "completed" || got.Progress.Percent != 100 {
		t.Fatalf("expected completed progress, got %#v", got.Progress)
	}
}

func TestSubmitAndIngestRepoSummaryWorker(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	runRoot := t.TempDir()
	repoRoot := filepath.Join(runRoot, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, "broker"), 0o755); err != nil {
		t.Fatalf("mkdir broker dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "deploy", "slurm"), 0o755); err != nil {
		t.Fatalf("mkdir deploy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "broker", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	actualRepoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	backend := &mutableFakeBackend{}
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		actualRepoRoot,
	)

	submitResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "repo_summary",
		InputRefs: []types.InputRef{
			{Type: "directory", URI: "file://" + repoRoot, ContentHash: "sha256:test"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	cmd := exec.Command(
		"python3",
		filepath.Join(actualRepoRoot, "workers", "repo-summary", "main.py"),
		"--job-spec", filepath.Join(jobDir, "job_spec.json"),
		"--input-manifest", filepath.Join(jobDir, "input_manifest.json"),
		"--output-dir", jobDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run repo worker: %v: %s", err, string(output))
	}

	backend.status = backends.RunStatus{
		BackendRunID: "run-1",
		State:        types.JobStateSucceeded,
		RawState:     "COMPLETED",
		ExitCode:     "0:0",
	}

	got, err := svc.GetJob(context.Background(), submitResp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded, got %q", got.State)
	}
	if got.Result == nil || got.Result.SchemaName != "repo_summary_v1" {
		t.Fatalf("expected repo_summary_v1 result, got %#v", got.Result)
	}
	if len(got.Artifacts) != 1 {
		t.Fatalf("expected one artifact, got %d", len(got.Artifacts))
	}
	if got.Progress == nil || got.Progress.State != "completed" || got.Progress.Percent != 100 {
		t.Fatalf("expected completed progress, got %#v", got.Progress)
	}
}

func TestSubmitAndIngestRAGCompressionWorker(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	runRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	inputPath := filepath.Join(runRoot, "build.log")
	inputText := "2026-06-26T12:01:00Z build started\nfatal error: generated/config.h: No such file or directory\n"
	if err := os.WriteFile(inputPath, []byte(inputText), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	backend := &mutableFakeBackend{}
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	submitResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "rag_compress",
		InputRefs: []types.InputRef{
			{Type: "log", URI: "file://" + inputPath, ContentHash: "sha256:test", Classification: "restricted"},
		},
		TaskParams: map[string]any{
			"query": "why did the build fail?",
		},
		Constraints: types.Constraints{
			RetrievedChunkBudget:      64000,
			PerChunkCompressionBudget: 384,
			FinalEvidencePackBudget:   4000,
			RemoteModelContextBudget:  12000,
		},
		OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	cmd := exec.Command(
		"python3",
		filepath.Join(repoRoot, "workers", "rag-compression", "main.py"),
		"--job-spec", filepath.Join(jobDir, "job_spec.json"),
		"--input-manifest", filepath.Join(jobDir, "input_manifest.json"),
		"--output-dir", jobDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run rag worker: %v: %s", err, string(output))
	}

	backend.status = backends.RunStatus{
		BackendRunID: "run-1",
		State:        types.JobStateSucceeded,
		RawState:     "COMPLETED",
		ExitCode:     "0:0",
	}

	got, err := svc.GetJob(context.Background(), submitResp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded, got %q", got.State)
	}
	if got.Result == nil || got.Result.SchemaName != "rag_evidence_pack_v1" {
		t.Fatalf("expected rag_evidence_pack_v1 result, got %#v", got.Result)
	}
	if got.RuntimeDiagnostics == nil {
		t.Fatalf("expected runtime diagnostics, got %#v", got.RuntimeDiagnostics)
	}
	if got.RuntimeDiagnostics["backend_mode"] == nil {
		t.Fatalf("expected runtime diagnostics backend_mode, got %#v", got.RuntimeDiagnostics)
	}
	if !got.DegradedLocalExecution {
		t.Fatalf("expected degraded_local_execution, got %#v", got)
	}
	if !got.RetryRecommended {
		t.Fatalf("expected retry_recommended for no-real-backend degraded run, got %#v", got)
	}
	if got.ExecutionQuality != "no_real_backend" {
		t.Fatalf("expected execution_quality no_real_backend, got %#v", got.ExecutionQuality)
	}
	if got.Result.Payload["query"] != "why did the build fail?" {
		t.Fatalf("unexpected query payload: %#v", got.Result.Payload)
	}
	evidence, _ := got.Result.Payload["evidence"].([]any)
	if len(evidence) == 0 {
		t.Fatalf("expected evidence, got %#v", got.Result.Payload)
	}
	if len(got.Artifacts) == 0 {
		t.Fatalf("expected artifacts, got %#v", got.Artifacts)
	}
	if !artifactTypesInclude(got.Artifacts, "retrieval_plan", "retrieval_trace", "chunk_manifest", "rerank_result", "evidence_pack", "retrieval_result", "validation_report") {
		t.Fatalf("expected staged rag artifacts, got %#v", got.Artifacts)
	}
	retrievalPlanPath := artifactPathForType(got.Artifacts, "retrieval_plan")
	if retrievalPlanPath == "" {
		t.Fatalf("expected retrieval plan artifact path, got %#v", got.Artifacts)
	}
	retrievalPlan := loadJSONFileForTest(t, retrievalPlanPath)
	effective, ok := retrievalPlan["effective_strategies"].([]any)
	if !ok || len(effective) == 0 {
		t.Fatalf("expected effective retrieval strategies, got %#v", retrievalPlan)
	}
	retrievalTracePath := artifactPathForType(got.Artifacts, "retrieval_trace")
	if retrievalTracePath == "" {
		t.Fatalf("expected retrieval trace artifact path, got %#v", got.Artifacts)
	}
	retrievalTrace := loadJSONFileForTest(t, retrievalTracePath)
	executions, ok := retrievalTrace["strategy_executions"].([]any)
	if !ok || len(executions) == 0 {
		t.Fatalf("expected strategy executions in retrieval trace, got %#v", retrievalTrace)
	}
	firstExecution, ok := executions[0].(map[string]any)
	if !ok || firstExecution["backend_mode"] == nil {
		t.Fatalf("expected backend mode in retrieval trace, got %#v", retrievalTrace)
	}
	policySignals, ok := retrievalTrace["policy_signals"].(map[string]any)
	if ok && len(policySignals) > 0 {
		t.Fatalf("did not expect retrieval trace to duplicate policy signals, got %#v", retrievalTrace)
	}
	validationPath := artifactPathForType(got.Artifacts, "validation_report")
	if validationPath == "" {
		t.Fatalf("expected validation report artifact path, got %#v", got.Artifacts)
	}
	validation := loadJSONFileForTest(t, validationPath)
	if validation["chunks_indexed"].(float64) < 1 {
		t.Fatalf("expected indexed chunks in validation report, got %#v", validation)
	}
	validationPolicy, ok := validation["policy_signals"].(map[string]any)
	if !ok || validationPolicy["mode_counts"] == nil {
		t.Fatalf("expected policy signals in validation report, got %#v", validation)
	}
	stages, ok := validation["pipeline_stages"].([]any)
	if !ok || len(stages) < 8 {
		t.Fatalf("expected pipeline stages in validation report, got %#v", validation)
	}
	if got.Progress == nil || got.Progress.State != "completed" || got.Progress.Percent != 100 {
		t.Fatalf("expected completed progress, got %#v", got.Progress)
	}
}

func TestSubmitAndIngestRAGCompressionWorkerTrimsToFinalBudget(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	runRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	inputPath := filepath.Join(runRoot, "build.log")
	inputText := strings.Repeat("fatal error: generated/config.h missing during build step\n", 120)
	if err := os.WriteFile(inputPath, []byte(inputText), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	backend := &mutableFakeBackend{}
	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	submitResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "rag_compress",
		InputRefs: []types.InputRef{
			{Type: "log", URI: "file://" + inputPath, ContentHash: "sha256:test", Classification: "restricted"},
		},
		TaskParams: map[string]any{
			"query": "why did the build fail?",
		},
		Constraints: types.Constraints{
			RetrievedChunkBudget:      64000,
			PerChunkCompressionBudget: 384,
			FinalEvidencePackBudget:   80,
			RemoteModelContextBudget:  12000,
		},
		OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	cmd := exec.Command(
		"python3",
		filepath.Join(repoRoot, "workers", "rag-compression", "main.py"),
		"--job-spec", filepath.Join(jobDir, "job_spec.json"),
		"--input-manifest", filepath.Join(jobDir, "input_manifest.json"),
		"--output-dir", jobDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run rag worker: %v: %s", err, string(output))
	}

	backend.status = backends.RunStatus{
		BackendRunID: "run-1",
		State:        types.JobStateSucceeded,
		RawState:     "COMPLETED",
		ExitCode:     "0:0",
	}

	got, err := svc.GetJob(context.Background(), submitResp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Result == nil {
		t.Fatal("expected result")
	}
	warnings, _ := got.Result.Payload["warnings"].([]any)
	if !containsAnyString(warnings, []string{"FINAL_EVIDENCE_PACK_TRIMMED"}) {
		t.Fatalf("expected trim warning, got %#v", got.Result.Payload)
	}
	budget, _ := got.Result.Payload["budget"].(map[string]any)
	if budget["final_pack_tokens"].(float64) > 80 {
		t.Fatalf("expected trimmed final budget <= 80, got %#v", budget)
	}
	retrieval, _ := got.Result.Payload["retrieval"].(map[string]any)
	if retrieval["strategy_hits"] == nil {
		t.Fatalf("expected strategy hits in retrieval payload, got %#v", got.Result.Payload)
	}
	if retrieval["strategy_stats"] == nil {
		t.Fatalf("expected strategy stats in retrieval payload, got %#v", got.Result.Payload)
	}
	retrievalTrace, _ := got.Result.Payload["retrieval_trace"].(map[string]any)
	if retrievalTrace["strategy_executions"] == nil {
		t.Fatalf("expected retrieval trace payload, got %#v", got.Result.Payload)
	}
	policySignals, _ := got.Result.Payload["policy_signals"].(map[string]any)
	if policySignals["mode_counts"] == nil {
		t.Fatalf("expected policy signals payload, got %#v", got.Result.Payload)
	}
	retrievalStats, _ := retrieval["strategy_stats"].([]any)
	if len(retrievalStats) == 0 {
		t.Fatalf("expected strategy stats payload, got %#v", got.Result.Payload)
	}
	firstStat, ok := retrievalStats[0].(map[string]any)
	if !ok || firstStat["backend_mode"] == nil {
		t.Fatalf("expected backend mode in strategy stats, got %#v", got.Result.Payload)
	}
}

func TestStageExecutionBundleResolvesArtifactInputs(t *testing.T) {
	runRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	artifactDir := filepath.Join(runRoot, "job_source")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	artifactPath := filepath.Join(artifactDir, "evidence_pack.json")
	if err := os.WriteFile(artifactPath, []byte(`{"evidence":[{"id":"ev_001","claim":"generated header missing"}]}`), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	now := time.Now().UTC()
	jobStore := store.NewMemoryJobStore()
	sourceJob := types.Job{
		ID:          "job_source",
		TaskType:    "rag_compress",
		State:       types.JobStateSucceeded,
		SubmittedBy: "alice",
		Request: types.SubmitJobRequest{
			TaskType:     "rag_compress",
			OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
		},
		Result: &types.Result{
			SchemaName:    "rag_evidence_pack_v1",
			SchemaVersion: "1.0.0",
			Payload:       map[string]any{"query": "why did the build fail?", "evidence": []any{}},
		},
		Artifacts: []types.Artifact{
			{ArtifactID: "artifact_evidence_pack", ArtifactType: "evidence_pack", Path: artifactPath, Classification: "restricted"},
		},
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
		SubmittedAt: now.Add(-time.Minute),
	}
	if err := jobStore.CreateJob(context.Background(), sourceJob); err != nil {
		t.Fatalf("create source job: %v", err)
	}

	backend := &mutableFakeBackend{}
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	submitResp, err := svc.SubmitJob(aliceUserCtx(), types.SubmitJobRequest{
		TaskType: "propose_patch",
		InputRefs: []types.InputRef{
			{Type: "artifact", URI: "artifact://artifact_evidence_pack"},
		},
		TaskParams: map[string]any{
			"problem":             "fix the generated header issue",
			"validation_commands": []any{"go test ./..."},
		},
		OutputSchema: types.OutputSchemaRef{Name: "patch_proposal_pack_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	inputManifest := loadJSONFileForTest(t, filepath.Join(jobDir, "input_manifest.json"))
	inputRefs, ok := inputManifest["input_refs"].([]any)
	if !ok || len(inputRefs) != 1 {
		t.Fatalf("unexpected input manifest: %#v", inputManifest)
	}
	firstRef := inputRefs[0].(map[string]any)
	metadata, ok := firstRef["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata in input manifest, got %#v", firstRef)
	}
	if metadata["resolved_path"] != artifactPath {
		t.Fatalf("expected resolved artifact path %q, got %#v", artifactPath, metadata)
	}
	if metadata["source_job_id"] != "job_source" {
		t.Fatalf("expected source_job_id=job_source, got %#v", metadata)
	}
}

func TestSubmitAndIngestProposePatchWorkerFromArtifactEvidence(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	runRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	artifactDir := filepath.Join(runRoot, "job_source")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	artifactPath := filepath.Join(artifactDir, "evidence_pack.json")
	if err := os.WriteFile(artifactPath, []byte(`{"evidence":[{"id":"ev_001","claim":"generated header missing","source_refs":[{"path":"broker/pkg/service/service.go","line_start":12,"line_end":34}]}]}`), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	now := time.Now().UTC()
	jobStore := store.NewMemoryJobStore()
	sourceJob := types.Job{
		ID:          "job_source",
		TaskType:    "rag_compress",
		State:       types.JobStateSucceeded,
		SubmittedBy: "alice",
		Request: types.SubmitJobRequest{
			TaskType:     "rag_compress",
			OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
		},
		Result: &types.Result{
			SchemaName:    "rag_evidence_pack_v1",
			SchemaVersion: "1.0.0",
			Payload:       map[string]any{"query": "why did the build fail?", "evidence": []any{}},
		},
		Artifacts: []types.Artifact{
			{ArtifactID: "artifact_evidence_pack", ArtifactType: "evidence_pack", Path: artifactPath, Classification: "restricted"},
		},
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
		SubmittedAt: now.Add(-time.Minute),
	}
	if err := jobStore.CreateJob(context.Background(), sourceJob); err != nil {
		t.Fatalf("create source job: %v", err)
	}

	backend := &mutableFakeBackend{}
	svc := New(
		jobStore,
		backend,
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	submitResp, err := svc.SubmitJob(aliceUserCtx(), types.SubmitJobRequest{
		TaskType: "propose_patch",
		InputRefs: []types.InputRef{
			{Type: "artifact", URI: "artifact://artifact_evidence_pack"},
		},
		TaskParams: map[string]any{
			"problem":             "fix the generated header issue",
			"validation_commands": []any{"go test ./..."},
			"allowed_paths":       []any{"broker/pkg/service"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "patch_proposal_pack_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	cmd := exec.Command(
		"python3",
		filepath.Join(repoRoot, "workers", "rag-compression", "main.py"),
		"--job-spec", filepath.Join(jobDir, "job_spec.json"),
		"--input-manifest", filepath.Join(jobDir, "input_manifest.json"),
		"--output-dir", jobDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run propose_patch worker: %v: %s", err, string(output))
	}

	backend.status = backends.RunStatus{
		BackendRunID: "run-1",
		State:        types.JobStateSucceeded,
		RawState:     "COMPLETED",
		ExitCode:     "0:0",
	}

	got, err := svc.GetJob(aliceUserCtx(), submitResp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Result == nil || got.Result.SchemaName != "patch_proposal_pack_v1" {
		t.Fatalf("expected patch_proposal_pack_v1 result, got %#v", got.Result)
	}
	patches, ok := got.Result.Payload["patches"].([]any)
	if !ok || len(patches) == 0 {
		t.Fatalf("expected patch proposals, got %#v", got.Result.Payload)
	}
	if !artifactTypesInclude(got.Artifacts, "retrieval_plan", "retrieval_trace", "chunk_manifest", "rerank_result", "evidence_pack", "retrieval_result", "patch_plan", "validation_report") {
		t.Fatalf("expected patch and validation artifacts, got %#v", got.Artifacts)
	}
}

func artifactPathForType(artifacts []types.Artifact, artifactType string) string {
	for _, artifact := range artifacts {
		if artifact.ArtifactType == artifactType {
			return artifact.Path
		}
	}
	return ""
}

func TestSubmitJobCacheHitForDocumentSummary(t *testing.T) {
	runRoot := t.TempDir()
	repoRoot := t.TempDir()
	inputPath := filepath.Join(runRoot, "doc.txt")
	if err := os.WriteFile(inputPath, []byte("same content"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	firstResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + inputPath},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit first job: %v", err)
	}

	now := time.Now().UTC()
	firstJob, err := jobStore.GetJob(context.Background(), firstResp.JobID)
	if err != nil {
		t.Fatalf("get first job: %v", err)
	}
	firstJob.State = types.JobStateSucceeded
	firstJob.Result = &types.Result{
		SchemaName:    "document_summary_v1",
		SchemaVersion: "1.0.0",
		Payload:       map[string]any{"summary": "cached"},
	}
	firstJob.Artifacts = []types.Artifact{{ArtifactID: "artifact_1", ArtifactType: "excerpt"}}
	firstJob.CompletedAt = &now
	firstJob.UpdatedAt = now
	if err := jobStore.UpdateJob(context.Background(), firstJob); err != nil {
		t.Fatalf("update first job: %v", err)
	}

	secondResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + inputPath},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit second job: %v", err)
	}
	if secondResp.Cache.Status != "hit" {
		t.Fatalf("expected cache hit, got %q", secondResp.Cache.Status)
	}

	secondJob, err := jobStore.GetJob(context.Background(), secondResp.JobID)
	if err != nil {
		t.Fatalf("get second job: %v", err)
	}
	if secondJob.State != types.JobStateSucceeded {
		t.Fatalf("expected succeeded state, got %q", secondJob.State)
	}
	if secondJob.BackendKind != "cache" {
		t.Fatalf("expected cache backend, got %q", secondJob.BackendKind)
	}
	if secondJob.Result == nil || secondJob.Result.Payload["summary"] != "cached" {
		t.Fatalf("expected cached result, got %#v", secondJob.Result)
	}
}

func TestSubmitJobCacheHitForRepoSummary(t *testing.T) {
	runRoot := t.TempDir()
	repoRoot := filepath.Join(runRoot, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, "broker"), 0o755); err != nil {
		t.Fatalf("mkdir broker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "broker", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}

	jobStore := store.NewMemoryJobStore()
	svc := New(
		jobStore,
		fakeBackend{},
		log.New(io.Discard, "", 0),
		runRoot,
		repoRoot,
	)

	firstResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "repo_summary",
		InputRefs: []types.InputRef{
			{Type: "directory", URI: "file://" + repoRoot},
		},
		OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit first job: %v", err)
	}

	now := time.Now().UTC()
	firstJob, err := jobStore.GetJob(context.Background(), firstResp.JobID)
	if err != nil {
		t.Fatalf("get first job: %v", err)
	}
	firstJob.State = types.JobStateSucceeded
	firstJob.Result = &types.Result{
		SchemaName:    "repo_summary_v1",
		SchemaVersion: "1.0.0",
		Payload:       map[string]any{"summary": "cached repo summary"},
	}
	firstJob.CompletedAt = &now
	firstJob.UpdatedAt = now
	if err := jobStore.UpdateJob(context.Background(), firstJob); err != nil {
		t.Fatalf("update first job: %v", err)
	}

	secondResp, err := svc.SubmitJob(context.Background(), types.SubmitJobRequest{
		TaskType: "repo_summary",
		InputRefs: []types.InputRef{
			{Type: "directory", URI: "file://" + repoRoot},
		},
		OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit second job: %v", err)
	}
	if secondResp.Cache.Status != "hit" {
		t.Fatalf("expected cache hit, got %q", secondResp.Cache.Status)
	}

	secondJob, err := jobStore.GetJob(context.Background(), secondResp.JobID)
	if err != nil {
		t.Fatalf("get second job: %v", err)
	}
	if secondJob.Result == nil || secondJob.Result.Payload["summary"] != "cached repo summary" {
		t.Fatalf("expected cached repo result, got %#v", secondJob.Result)
	}
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsAnyString(items []any, needles []string) bool {
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		for _, needle := range needles {
			if text == needle {
				return true
			}
		}
	}
	return false
}

func artifactTypesInclude(artifacts []types.Artifact, required ...string) bool {
	if len(required) == 0 {
		return true
	}
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		seen[artifact.ArtifactType] = struct{}{}
	}
	for _, want := range required {
		if _, ok := seen[want]; !ok {
			return false
		}
	}
	return true
}

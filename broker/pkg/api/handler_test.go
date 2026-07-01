package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/audit"
	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/backends"
	"github.com/limr/ollama-slurm/broker/pkg/backends/slurm"
	"github.com/limr/ollama-slurm/broker/pkg/cache"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

const (
	httpRetryBudgetExceededMessage   = "cumulative retried_shards=2 would exceed non-admin limit 1"
	httpReleaseBudgetExceededMessage = "cumulative forced_release_batches=2 would exceed non-admin limit 1"
)

func TestSubmitAndFetchJob(t *testing.T) {
	handler := newTestHandler()

	body := mustJSON(t, types.SubmitJobRequest{
		TaskType: "log_analysis",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file:///workspace/build.log"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submitResp.JobID == "" {
		t.Fatal("expected job ID")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+submitResp.JobID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, getRec.Code, getRec.Body.String())
	}
}

func TestGetJobIncludesRuntimeDiagnostics(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		t.TempDir(),
		".",
	)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_runtime_status",
		TaskType: "rag_compress",
		State:    types.JobStateSucceeded,
		Request: types.SubmitJobRequest{
			TaskType:     "rag_compress",
			OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
		},
		RuntimeDiagnostics: map[string]any{
			"backend_name":        "llama.cpp",
			"backend_mode":        "unavailable",
			"selected_model":      "gpt-oss-20b.p40",
			"resource_tier":       "p40-rag-compression",
			"endpoint_configured": true,
			"last_error":          "connection refused",
		},
		ExecutionQuality:       "no_real_backend",
		DegradedLocalExecution: true,
		RetryRecommended:       true,
		CreatedAt:              now,
		UpdatedAt:              now,
		SubmittedAt:            now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var got types.Job
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if got.RuntimeDiagnostics["backend_mode"] != "unavailable" {
		t.Fatalf("expected runtime diagnostics on get job, got %#v", got.RuntimeDiagnostics)
	}
	if !got.DegradedLocalExecution || !got.RetryRecommended || got.ExecutionQuality != "no_real_backend" {
		t.Fatalf("expected summary flags on get job, got %#v", got)
	}
}

func TestCancelJob(t *testing.T) {
	handler := newTestHandler()

	body := mustJSON(t, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/jobs/"+submitResp.JobID+":cancel", nil)
	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, cancelRec.Code, cancelRec.Body.String())
	}
}

func TestRAGAliasSubmit(t *testing.T) {
	handler := newTestHandler()

	body := mustJSON(t, map[string]any{
		"query": "why did the build fail?",
		"input_refs": []map[string]any{
			{"type": "log", "uri": "file:///workspace/build.log"},
		},
		"constraints": map[string]any{
			"retrieved_chunk_budget": 64000,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/rag/compressions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submitResp.JobID == "" {
		t.Fatal("expected job ID")
	}
}

func TestRAGAliasPreservesTopLevelQueryInStoredJob(t *testing.T) {
	handler, _ := newTestHandlerWithRunRoot(t)

	body := mustJSON(t, map[string]any{
		"query":                "why did the build fail?",
		"retrieval_strategies": []any{"ripgrep", "bm25"},
		"input_refs": []map[string]any{
			{"type": "log", "uri": "file:///workspace/build.log"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/rag/compressions", bytes.NewReader(body))
	setBrokerIdentity(req, "alice", "user")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+submitResp.JobID, nil)
	setBrokerIdentity(getReq, "alice", "user")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, getRec.Code, getRec.Body.String())
	}

	var job types.Job
	if err := json.NewDecoder(getRec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.Request.TaskParams["query"] != "why did the build fail?" {
		t.Fatalf("expected normalized query task param, got %#v", job.Request.TaskParams)
	}
	if strategies, ok := job.Request.TaskParams["retrieval_strategies"].([]any); ok && len(strategies) > 0 {
	} else {
		t.Fatalf("expected normalized retrieval strategies, got %#v", job.Request.TaskParams)
	}
}

func TestRAGEvidencePackMetadataEndpoint(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())

	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_rag_meta",
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
			Payload:       map[string]any{"query": "why", "evidence": []any{}},
		},
		Artifacts: []types.Artifact{
			{ArtifactID: "artifact_evidence_pack", ArtifactType: "evidence_pack", Classification: "restricted"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/rag/evidence-packs/artifact_evidence_pack/metadata", nil)
	setBrokerIdentity(req, "alice", "user")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var meta types.ArtifactMetadata
	if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if meta.ArtifactID != "artifact_evidence_pack" || meta.ArtifactType != "evidence_pack" || meta.SourceJobID != "job_rag_meta" {
		t.Fatalf("unexpected metadata: %#v", meta)
	}
}

func TestRAGIndexMetadataEndpoint(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())

	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_repo_index",
		TaskType:    "inspect_repo",
		State:       types.JobStateSucceeded,
		SubmittedBy: "alice",
		Request: types.SubmitJobRequest{
			TaskType:     "inspect_repo",
			OutputSchema: types.OutputSchemaRef{Name: "repo_inspection_pack_v1"},
		},
		Result: &types.Result{
			SchemaName:    "repo_inspection_pack_v1",
			SchemaVersion: "1.0.0",
			Payload:       map[string]any{"query": "inspect", "evidence": []any{}},
		},
		Artifacts: []types.Artifact{
			{ArtifactID: "artifact_repo_index", ArtifactType: "retrieval_result", Classification: "restricted"},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/rag/indexes/artifact_repo_index/metadata", nil)
	setBrokerIdentity(req, "alice", "user")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var meta types.ArtifactMetadata
	if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if meta.ArtifactID != "artifact_repo_index" || meta.ArtifactType != "retrieval_result" || meta.SourceTaskType != "inspect_repo" {
		t.Fatalf("unexpected metadata: %#v", meta)
	}
}

func TestRAGCacheLookupEndpointHit(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())

	inputPath := filepath.Join(t.TempDir(), "build.log")
	if err := os.WriteFile(inputPath, []byte("fatal error: generated header missing\n"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	reqShape := types.SubmitJobRequest{
		TaskType: "rag_compress",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + inputPath, Classification: "restricted"},
		},
		TaskParams:   map[string]any{"query": "why did the build fail?"},
		OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
	}
	cacheKey, cacheable, err := cache.KeyForRequest(reqShape)
	if err != nil || !cacheable {
		t.Fatalf("expected cacheable request, err=%v cacheable=%v", err, cacheable)
	}

	now := time.Now().UTC()
	cachedJob := types.Job{
		ID:          "job_cached_rag",
		TaskType:    "rag_compress",
		State:       types.JobStateSucceeded,
		SubmittedBy: "alice",
		Request:     reqShape,
		Result: &types.Result{
			SchemaName:    "rag_evidence_pack_v1",
			SchemaVersion: "1.0.0",
			Payload:       map[string]any{"query": "why did the build fail?", "evidence": []any{}},
		},
		CacheKey:    cacheKey,
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), cachedJob); err != nil {
		t.Fatalf("create cached job: %v", err)
	}

	body := mustJSON(t, reqShape)
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/cache:lookup", bytes.NewReader(body))
	setBrokerIdentity(req, "alice", "user")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var resp types.CacheLookupResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode cache lookup: %v", err)
	}
	if resp.Status != "hit" || resp.SourceJobID != "job_cached_rag" {
		t.Fatalf("unexpected cache response: %#v", resp)
	}
}

func TestListJobs(t *testing.T) {
	handler := newTestHandler()

	body := mustJSON(t, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	listReq := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, listRec.Code, listRec.Body.String())
	}
}

func TestGetRootJobStatus(t *testing.T) {
	handler, _ := newTestHandlerWithRunRoot(t)

	body := mustJSON(t, types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children: []types.ParallelChildRequest{
			{InputRefs: []types.InputRef{{Type: "file", URI: "file:///tmp/a.txt"}}, ShardIndex: 0, ShardCount: 2},
			{InputRefs: []types.InputRef{{Type: "file", URI: "file:///tmp/b.txt"}}, ShardIndex: 1, ShardCount: 2},
		},
		Reducer: &types.ParallelReducerRequest{
			TaskType:     "document_summary",
			OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}

	var submitResp types.SubmitParallelJobsResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/roots/"+submitResp.RootJobID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, getRec.Code, getRec.Body.String())
	}
	var rootStatus types.RootJobStatus
	if err := json.NewDecoder(getRec.Body).Decode(&rootStatus); err != nil {
		t.Fatalf("decode root status: %v", err)
	}
	if rootStatus.RootJobID != submitResp.RootJobID {
		t.Fatalf("unexpected root status: %#v", rootStatus)
	}
}

func TestRetryFailedRootShards(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.NewWithAuditAndOptions(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		audit.NewNopLogger(),
		runRoot,
		".",
		service.Options{RootActionMaxRetriedShards: 2},
	)
	now := time.Now().UTC()
	for _, job := range []types.Job{
		{
			ID: "job_ok", TaskType: "document_summary", State: types.JobStateSucceeded, RootJobID: "root_retry_http",
			SubmittedBy:   "alice",
			Request:       types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
			Orchestration: &types.OrchestrationInfo{RootJobID: "root_retry_http", Strategy: "fanout_child", ShardKey: "doc:a", ShardIndex: 0, ShardCount: 2},
			CreatedAt:     now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute), SubmittedAt: now.Add(-2 * time.Minute),
		},
		{
			ID: "job_failed", TaskType: "document_summary", State: types.JobStateFailed, RootJobID: "root_retry_http",
			SubmittedBy:   "alice",
			Request:       types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
			Orchestration: &types.OrchestrationInfo{RootJobID: "root_retry_http", Strategy: "fanout_child", ShardKey: "doc:b", ShardIndex: 1, ShardCount: 2},
			CreatedAt:     now.Add(-1 * time.Minute), UpdatedAt: now.Add(-1 * time.Minute), SubmittedAt: now.Add(-1 * time.Minute),
		},
	} {
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())

	req := httptest.NewRequest(http.MethodPost, "/v1/roots/root_retry_http:retry-failed", bytes.NewReader(mustJSON(t, map[string]any{})))
	setBrokerIdentity(req, "alice", "user")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}
	var resp types.RetryFailedRootShardsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if resp.RetriedCount != 1 {
		t.Fatalf("expected one retried shard, got %#v", resp)
	}
	if resp.CumulativeRetriedShards != 1 || resp.RemainingRetriedShardBudget != 1 {
		t.Fatalf("expected direct retry budget counters, got %#v", resp)
	}
}

func TestReleaseDeferredRootChunks(t *testing.T) {
	svc := newAPIThrottledBatchService(t, 2)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())
	submitReq := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(mustJSON(t, types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     throttledFourChildRequests(),
	})))
	setBrokerIdentity(submitReq, "alice", "user")
	submitRec := httptest.NewRecorder()
	handler.ServeHTTP(submitRec, submitReq)
	if submitRec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, submitRec.Code, submitRec.Body.String())
	}
	var submitResp types.SubmitParallelJobsResponse
	if err := json.NewDecoder(submitRec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/roots/"+submitResp.RootJobID+":release-deferred", bytes.NewReader(mustJSON(t, map[string]any{
		"max_additional_batches": 1,
	})))
	setBrokerIdentity(req, "alice", "user")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}
	var resp types.ReleaseDeferredRootChunksResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode release response: %v", err)
	}
	if resp.ReleasedChunks != 1 {
		t.Fatalf("expected one released chunk, got %#v", resp)
	}
	if resp.CumulativeForcedReleaseChunks != 1 || resp.RemainingForcedReleaseBudget != 1 {
		t.Fatalf("expected direct forced-release budget counters, got %#v", resp)
	}
}

func TestRetryFailedRootShardsReturnsForbiddenWhenCumulativeBudgetExceeded(t *testing.T) {
	handler, rootJobID := newHTTPRetryBudgetExceededFixture(t)
	secondReq := httptest.NewRequest(http.MethodPost, "/v1/roots/"+rootJobID+":retry-failed", bytes.NewReader(mustJSON(t, map[string]any{})))
	setBrokerIdentity(secondReq, "alice", "user")
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d: %s", http.StatusForbidden, secondRec.Code, secondRec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(secondRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	assertForbiddenErrorResponse(t, errResp.Error.Code, errResp.Error.Message, httpRetryBudgetExceededMessage)
}

func TestReleaseDeferredRootChunksReturnsForbiddenWhenCumulativeBudgetExceeded(t *testing.T) {
	handler, rootJobID := newHTTPReleaseBudgetExceededFixture(t)
	secondReq := httptest.NewRequest(http.MethodPost, "/v1/roots/"+rootJobID+":release-deferred", bytes.NewReader(mustJSON(t, map[string]any{
		"max_additional_batches": 1,
	})))
	setBrokerIdentity(secondReq, "alice", "user")
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d: %s", http.StatusForbidden, secondRec.Code, secondRec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(secondRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	assertForbiddenErrorResponse(t, errResp.Error.Code, errResp.Error.Message, httpReleaseBudgetExceededMessage)
}

func TestFetchLogs(t *testing.T) {
	handler, runRoot := newTestHandlerWithRunRoot(t)

	body := mustJSON(t, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	jobDir := filepath.Join(runRoot, submitResp.JobID)
	if err := os.WriteFile(filepath.Join(jobDir, "stderr.log"), []byte("Bearer super-secret\n"), 0o644); err != nil {
		t.Fatalf("write stderr: %v", err)
	}

	logReq := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+submitResp.JobID+"/logs?stream=stderr&max_bytes=128", nil)
	logRec := httptest.NewRecorder()
	handler.ServeHTTP(logRec, logReq)

	if logRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, logRec.Code, logRec.Body.String())
	}
	if bytes.Contains(logRec.Body.Bytes(), []byte("super-secret")) {
		t.Fatalf("expected redacted response, got %s", logRec.Body.String())
	}
}

func TestFetchLogsPolicyDenied(t *testing.T) {
	handler, _ := newTestHandlerWithRunRoot(t)

	body := mustJSON(t, types.SubmitJobRequest{
		TaskType: "log_analysis",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file:///workspace/build.log", Classification: "restricted"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	logReq := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+submitResp.JobID+"/logs", nil)
	logRec := httptest.NewRecorder()
	handler.ServeHTTP(logRec, logReq)

	if logRec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d: %s", http.StatusForbidden, logRec.Code, logRec.Body.String())
	}
}

func TestFetchResultAppliesReleasePolicy(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_http_result_policy",
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
					map[string]any{"path": "broker/main.go", "kind": "service_entrypoint"},
				},
			},
		},
		RuntimeDiagnostics: map[string]any{
			"backend_name":        "llama.cpp",
			"backend_mode":        "unavailable",
			"endpoint_configured": true,
			"last_error":          "connection refused",
		},
		ExecutionQuality:       "no_real_backend",
		DegradedLocalExecution: true,
		RetryRecommended:       true,
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

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID+"/result", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var release types.JobResultRelease
	if err := json.NewDecoder(rec.Body).Decode(&release); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	entrypoints := release.Result.Payload["entrypoints"].([]any)
	first := entrypoints[0].(map[string]any)
	if first["path"] != "[REDACTED]" {
		t.Fatalf("expected redacted path, got %#v", first["path"])
	}
	if len(release.Artifacts) != 0 {
		t.Fatalf("expected withheld artifacts, got %#v", release.Artifacts)
	}
	if release.RuntimeDiagnostics["backend_mode"] != "unavailable" {
		t.Fatalf("expected runtime diagnostics in release, got %#v", release.RuntimeDiagnostics)
	}
	if !release.DegradedLocalExecution || !release.RetryRecommended || release.ExecutionQuality != "no_real_backend" {
		t.Fatalf("expected summary flags in release, got %#v", release)
	}
}

func TestGetJobForbiddenForDifferentHTTPActor(t *testing.T) {
	handler, _ := newTestHandlerWithRunRoot(t)

	body := mustJSON(t, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	setBrokerIdentity(req, "alice", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+submitResp.JobID, nil)
	setBrokerIdentity(getReq, "bob", "")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d: %s", http.StatusForbidden, getRec.Code, getRec.Body.String())
	}
}

func TestGetRetryRecommendationAndRetryRecommendedJob(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())

	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_http_retry_rec",
		TaskType:    "rag_compress",
		State:       types.JobStateSucceeded,
		SubmittedBy: "alice",
		Request: types.SubmitJobRequest{
			TaskType:     "rag_compress",
			OutputSchema: types.OutputSchemaRef{Name: "rag_evidence_pack_v1"},
			ExecutionProfile: types.ExecutionProfile{
				Backend: "slurm",
				Tier:    "p40-rag-compression",
				Runtime: "llama.cpp",
			},
		},
		Result: &types.Result{
			SchemaName:    "rag_evidence_pack_v1",
			SchemaVersion: "1.0.0",
			Payload: map[string]any{
				"query": "why did the build fail?",
				"broker_retry_recommendation": map[string]any{
					"recommended": true,
					"reason":      "no_real_retrieval_backend",
					"task_type":   "rag_compress",
					"execution_profile": map[string]any{
						"backend": "slurm",
						"tier":    "a100-reasoning",
						"runtime": "llama.cpp",
					},
					"placement_hint": map[string]any{
						"backend_preference": "slurm",
						"tier_preference":    "a100-reasoning",
						"qos":                "scavenger",
						"preemptible":        true,
					},
				},
			},
		},
		ResultError: "broker_policy_no_real_retrieval_backend",
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
		CompletedAt: &now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	recReq := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID+"/retry-recommendation", nil)
	setBrokerIdentity(recReq, "alice", "user")
	recRec := httptest.NewRecorder()
	handler.ServeHTTP(recRec, recReq)
	if recRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, recRec.Code, recRec.Body.String())
	}
	var rec types.JobRetryRecommendation
	if err := json.NewDecoder(recRec.Body).Decode(&rec); err != nil {
		t.Fatalf("decode recommendation: %v", err)
	}
	if rec.ExecutionProfile.Tier != "a100-reasoning" {
		t.Fatalf("expected a100 retry recommendation, got %#v", rec)
	}
	if rec.PlacementHint.TierPreference != "a100-reasoning" || !rec.PlacementHint.Preemptible {
		t.Fatalf("expected placement hint on recommendation, got %#v", rec)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/v1/jobs/"+job.ID+":retry-recommended", nil)
	setBrokerIdentity(retryReq, "alice", "user")
	retryRec := httptest.NewRecorder()
	handler.ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, retryRec.Code, retryRec.Body.String())
	}
	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(retryRec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode retry submit response: %v", err)
	}
	retriedJob, err := jobStore.GetJob(context.Background(), submitResp.JobID)
	if err != nil {
		t.Fatalf("get retried job: %v", err)
	}
	if retriedJob.Request.ExecutionProfile.Tier != "a100-reasoning" {
		t.Fatalf("expected retried job to use recommended tier, got %#v", retriedJob.Request.ExecutionProfile)
	}
	if retriedJob.Request.ExecutionProfile.QOS != "scavenger" {
		t.Fatalf("expected retried job to use recommended qos, got %#v", retriedJob.Request.ExecutionProfile)
	}
	if retriedJob.Request.TaskParams["_broker_retry_qos"] != "scavenger" || retriedJob.Request.TaskParams["_broker_retry_preemptible"] != true {
		t.Fatalf("expected placement hint merged into task params, got %#v", retriedJob.Request.TaskParams)
	}
}

func TestListJobsFiltersByHTTPActor(t *testing.T) {
	handler, _ := newTestHandlerWithRunRoot(t)

	for _, actor := range []string{"alice", "bob"} {
		body := mustJSON(t, types.SubmitJobRequest{
			TaskType:     "document_summary",
			OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
		setBrokerIdentity(req, actor, "")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("submit for %s failed: %d %s", actor, rec.Code, rec.Body.String())
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	setBrokerIdentity(listReq, "alice", "")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, listRec.Code, listRec.Body.String())
	}
	var payload struct {
		Jobs  []types.Job `json:"jobs"`
		Count int         `json:"count"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if payload.Count != 1 || len(payload.Jobs) != 1 || payload.Jobs[0].SubmittedBy != "alice" {
		t.Fatalf("unexpected filtered list payload: %#v", payload)
	}
}

func TestAuditHealthRequiresAdmin(t *testing.T) {
	handler := newAuditHandler(t, false)

	req := httptest.NewRequest(http.MethodGet, "/v1/system/audit-health", nil)
	setBrokerIdentity(req, "alice", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestAuditHealthReportsValidChain(t *testing.T) {
	handler := newAuditHandler(t, false)

	req := httptest.NewRequest(http.MethodGet, "/v1/system/audit-health", nil)
	setBrokerIdentity(req, "ops", "admin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var result audit.VerificationResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode verification result: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid audit health, got %#v", result)
	}
}

func TestAuditHealthReportsBrokenChain(t *testing.T) {
	handler := newAuditHandler(t, true)

	req := httptest.NewRequest(http.MethodGet, "/v1/system/audit-health", nil)
	setBrokerIdentity(req, "ops", "admin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
	var result audit.VerificationResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode verification result: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected invalid audit health, got %#v", result)
	}
}

func TestStaticTokenAuthRejectsMissingBearerToken(t *testing.T) {
	handler := newStaticTokenTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestStaticTokenAuthUsesMappedPrincipal(t *testing.T) {
	handler := newStaticTokenTestHandler(t)

	body := mustJSON(t, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token-alice")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}

	var submitResp types.SubmitJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+submitResp.JobID, nil)
	getReq.Header.Set("Authorization", "Bearer token-bob")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d: %s", http.StatusForbidden, getRec.Code, getRec.Body.String())
	}
}

func TestStaticTokenAuthAdminCanListAllJobs(t *testing.T) {
	handler := newStaticTokenTestHandler(t)

	for _, token := range []string{"token-alice", "token-bob"} {
		body := mustJSON(t, types.SubmitJobRequest{
			TaskType:     "document_summary",
			OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("submit failed: %d %s", rec.Code, rec.Body.String())
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	listReq.Header.Set("Authorization", "Bearer token-admin")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, listRec.Code, listRec.Body.String())
	}
	var payload struct {
		Jobs  []types.Job `json:"jobs"`
		Count int         `json:"count"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if payload.Count != 2 {
		t.Fatalf("expected 2 jobs, got %#v", payload)
	}
}

func newTestHandler() *Handler {
	handler, _ := newTestHandlerWithRunRoot(nil)
	return handler
}

func newTestHandlerWithRunRoot(t *testing.T) (*Handler, string) {
	runRoot := ".broker/runs"
	if t != nil {
		runRoot = t.TempDir()
	}
	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	return NewHandler(svc, auth.NewHeaderAuthenticator()), runRoot
}

func newStaticTokenTestHandler(t *testing.T) *Handler {
	tokens := map[string]auth.Principal{
		"token-alice": {Actor: "alice", Role: "user"},
		"token-bob":   {Actor: "bob", Role: "user"},
		"token-admin": {Actor: "admin", Role: "admin"},
	}
	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		t.TempDir(),
		".",
	)
	return NewHandler(svc, auth.NewStaticTokenAuthenticator(tokens))
}

func newAuditHandler(t *testing.T, tamper bool) *Handler {
	t.Helper()
	runRoot := t.TempDir()
	auditPath := filepath.Join(runRoot, "audit.jsonl")
	auditLogger := audit.NewFileLogger(auditPath)
	if err := auditLogger.Log(context.Background(), audit.Event{Actor: "alice", Action: "job.submit", Outcome: "success"}); err != nil {
		t.Fatalf("log first audit event: %v", err)
	}
	if err := auditLogger.Log(context.Background(), audit.Event{Actor: "alice", Action: "job.get_status", Outcome: "success"}); err != nil {
		t.Fatalf("log second audit event: %v", err)
	}
	if tamper {
		data, err := os.ReadFile(auditPath)
		if err != nil {
			t.Fatalf("read audit file: %v", err)
		}
		tampered := bytes.Replace(data, []byte(`"outcome":"success"`), []byte(`"outcome":"forbidden"`), 1)
		if err := os.WriteFile(auditPath, tampered, 0o644); err != nil {
			t.Fatalf("write tampered audit file: %v", err)
		}
	}

	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	return NewHandlerWithAudit(svc, auth.NewHeaderAuthenticator(), auditPath)
}

func assertForbiddenErrorResponse(t *testing.T, code, message, expectedMessage string) {
	t.Helper()
	if code != "FORBIDDEN" || !strings.Contains(message, expectedMessage) {
		t.Fatalf("unexpected forbidden payload: code=%q message=%q expected_substring=%q", code, message, expectedMessage)
	}
}

func setBrokerIdentity(req *http.Request, actor, role string) {
	req.Header.Set("X-Broker-Actor", actor)
	if role != "" {
		req.Header.Set("X-Broker-Role", role)
	}
}

func newAPIThrottledBatchService(t *testing.T, releaseBudget int) *service.Service {
	t.Helper()
	return service.NewWithAuditAndOptions(
		store.NewMemoryJobStore(),
		&serviceTestBatchBackendAdapter{status: backends.RunStatus{State: types.JobStateQueued, RawState: "PENDING"}},
		log.New(io.Discard, "", 0),
		audit.NewNopLogger(),
		t.TempDir(),
		".",
		service.Options{
			ParallelMaxBatchSize:           2,
			ParallelMaxActiveBatches:       1,
			RootActionMaxAdditionalBatches: releaseBudget,
		},
	)
}

func newHTTPRetryBudgetExceededFixture(t *testing.T) (*Handler, string) {
	t.Helper()
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.NewWithAuditAndOptions(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		audit.NewNopLogger(),
		runRoot,
		".",
		service.Options{RootActionMaxRetriedShards: 1},
	)
	now := time.Now().UTC()
	rootJobID := "root_retry_http_cap"
	job := types.Job{
		ID: "job_failed_once", TaskType: "document_summary", State: types.JobStateFailed, RootJobID: rootJobID,
		SubmittedBy: "alice",
		Request:     types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
		Orchestration: &types.OrchestrationInfo{
			RootJobID: rootJobID, Strategy: "fanout_child", ShardIndex: 0, ShardCount: 1,
		},
		CreatedAt: now, UpdatedAt: now, SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())
	firstReq := httptest.NewRequest(http.MethodPost, "/v1/roots/"+rootJobID+":retry-failed", bytes.NewReader(mustJSON(t, map[string]any{})))
	setBrokerIdentity(firstReq, "alice", "user")
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, firstRec.Code, firstRec.Body.String())
	}
	var firstResp types.RetryFailedRootShardsResponse
	if err := json.NewDecoder(firstRec.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first retry response: %v", err)
	}
	retriedJob, err := jobStore.GetJob(context.Background(), firstResp.RetriedShards[0].JobID)
	if err != nil {
		t.Fatalf("get retried job: %v", err)
	}
	retriedJob.State = types.JobStateFailed
	retriedJob.BackendRunID = ""
	if err := jobStore.UpdateJob(context.Background(), retriedJob); err != nil {
		t.Fatalf("mark retried job failed again: %v", err)
	}
	return handler, rootJobID
}

func newHTTPReleaseBudgetExceededFixture(t *testing.T) (*Handler, string) {
	t.Helper()
	svc := newAPIThrottledBatchService(t, 1)
	handler := NewHandler(svc, auth.NewHeaderAuthenticator())
	submitReq := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(mustJSON(t, types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     throttledSixChildRequests(),
	})))
	setBrokerIdentity(submitReq, "alice", "user")
	submitRec := httptest.NewRecorder()
	handler.ServeHTTP(submitRec, submitReq)
	if submitRec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, submitRec.Code, submitRec.Body.String())
	}
	var submitResp types.SubmitParallelJobsResponse
	if err := json.NewDecoder(submitRec.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	firstReq := httptest.NewRequest(http.MethodPost, "/v1/roots/"+submitResp.RootJobID+":release-deferred", bytes.NewReader(mustJSON(t, map[string]any{
		"max_additional_batches": 1,
	})))
	setBrokerIdentity(firstReq, "alice", "user")
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, firstRec.Code, firstRec.Body.String())
	}
	return handler, submitResp.RootJobID
}

type serviceTestBatchBackendAdapter struct {
	status     backends.RunStatus
	batchCalls int
}

func (f *serviceTestBatchBackendAdapter) Name() string { return "api-fake-batch" }

func (f *serviceTestBatchBackendAdapter) SubmitRun(context.Context, types.Job) (backends.SubmitResponse, error) {
	return backends.SubmitResponse{
		BackendKind:  "api-fake-batch",
		BackendRunID: "single-run-1",
		InitialState: types.JobStateQueued,
	}, nil
}

func (f *serviceTestBatchBackendAdapter) SubmitRunBatch(_ context.Context, jobs []types.Job) ([]backends.SubmitResponse, error) {
	f.batchCalls++
	responses := make([]backends.SubmitResponse, 0, len(jobs))
	for i := range jobs {
		responses = append(responses, backends.SubmitResponse{
			BackendKind:  "api-fake-batch",
			BackendRunID: "batch-run-" + string(rune('0'+i)),
			InitialState: types.JobStateQueued,
		})
	}
	return responses, nil
}

func (f *serviceTestBatchBackendAdapter) GetRun(context.Context, string) (backends.RunStatus, error) {
	return f.status, nil
}

func (f *serviceTestBatchBackendAdapter) CancelRun(context.Context, string) error { return nil }

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return body
}

func throttledSixChildRequests() []types.ParallelChildRequest {
	children := make([]types.ParallelChildRequest, 0, 6)
	for i := 0; i < 6; i++ {
		children = append(children, types.ParallelChildRequest{
			InputRefs:  []types.InputRef{{Type: "file", URI: "file:///tmp/" + string(rune('a'+i)) + ".txt"}},
			ShardIndex: i,
			ShardCount: 6,
		})
	}
	return children
}

func throttledFourChildRequests() []types.ParallelChildRequest {
	children := make([]types.ParallelChildRequest, 0, 4)
	for i := 0; i < 4; i++ {
		children = append(children, types.ParallelChildRequest{
			InputRefs:  []types.InputRef{{Type: "file", URI: "file:///tmp/" + string(rune('a'+i)) + ".txt"}},
			ShardIndex: i,
			ShardCount: 4,
		})
	}
	return children
}

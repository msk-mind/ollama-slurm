package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/backends"
	"github.com/limr/ollama-slurm/broker/pkg/backends/slurm"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

const (
	mcpRetryBudgetExceededMessage   = "cumulative retried_shards=2 would exceed non-admin limit 1"
	mcpReleaseBudgetExceededMessage = "cumulative forced_release_batches=2 would exceed non-admin limit 1"
)

func mcpTestPrincipal() auth.Principal {
	return auth.Principal{Actor: "mcp:test", Role: "user"}
}

func TestToolsList(t *testing.T) {
	server := newTestServer()
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	tools := result["tools"].([]map[string]any)
	if len(tools) != 17 {
		t.Fatalf("expected 17 tools, got %d", len(tools))
	}
}

func TestSubmitToolCall(t *testing.T) {
	server := newTestServer()
	params := map[string]any{
		"name": "submit_local_job",
		"arguments": map[string]any{
			"task_type": "document_summary",
			"input_refs": []map[string]any{
				{"type": "file", "uri": "file:///tmp/does-not-exist.txt"},
			},
			"output_schema": map[string]any{"name": "document_summary_v1"},
		},
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramBytes,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if _, ok := result["structuredContent"]; !ok {
		t.Fatal("expected structuredContent")
	}
}

func TestServeStdioInitialize(t *testing.T) {
	server := newTestServer()
	in := bytes.NewBufferString(frameJSON(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"auth":{"actor":"alice","role":"user"}}}`))
	out := &bytes.Buffer{}
	if err := server.ServeStdio(context.Background(), in, out); err != nil {
		t.Fatalf("serve stdio: %v", err)
	}
	if !strings.Contains(out.String(), "Content-Length:") {
		t.Fatalf("expected framed response, got %q", out.String())
	}
}

func TestServeStdioInitializeNDJSON(t *testing.T) {
	server := newTestServer()
	in := bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"auth\":{\"actor\":\"alice\",\"role\":\"user\"}}}\n")
	out := &bytes.Buffer{}
	if err := server.ServeStdio(context.Background(), in, out); err != nil {
		t.Fatalf("serve stdio: %v", err)
	}
	if strings.Contains(out.String(), "Content-Length:") {
		t.Fatalf("expected ndjson response, got %q", out.String())
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("expected newline-delimited response, got %q", out.String())
	}
}

func TestServeStdioEndToEndToolFlow(t *testing.T) {
	runRoot := t.TempDir()
	inputPath := filepath.Join(runRoot, "doc.txt")
	if err := os.WriteFile(inputPath, []byte("MCP protocol test document.\n- point\n"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	server := NewServer(svc, auth.Principal{})

	messages := strings.Join([]string{
		frameJSON(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"auth":{"actor":"alice","role":"user"}}}`),
		frameJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`),
		frameJSON(fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"submit_local_job","arguments":{"task_type":"document_summary","input_refs":[{"type":"file","uri":"file://%s"}],"output_schema":{"name":"document_summary_v1"}}}}`, inputPath)),
	}, "")

	in := bytes.NewBufferString(messages)
	out := &bytes.Buffer{}
	if err := server.ServeStdio(context.Background(), in, out); err != nil {
		t.Fatalf("serve stdio: %v", err)
	}

	responses := decodeFramedResponses(t, out.Bytes())
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}

	var submitResp response
	if err := json.Unmarshal(responses[2], &submitResp); err != nil {
		t.Fatalf("unmarshal submit response: %v", err)
	}
	if submitResp.Error != nil {
		t.Fatalf("unexpected submit error: %#v", submitResp.Error)
	}

	result := submitResp.Result.(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["job_id"] == "" {
		t.Fatal("expected job_id in structured content")
	}
}

func TestListLocalCapabilitiesToolCall(t *testing.T) {
	server := newTestServer()
	initResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params:  mustRawJSON(t, `{"auth":{"actor":"alice","role":"user"}}`),
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}
	params := map[string]any{
		"name":      "list_local_capabilities",
		"arguments": map[string]any{},
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramBytes,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	taskTypes := structured["task_types"].([]map[string]any)
	if len(taskTypes) != 8 {
		t.Fatalf("expected 8 task types, got %d", len(taskTypes))
	}
	orchestration := structured["orchestration"].(map[string]any)
	if orchestration["independent_parallel_jobs"] != true {
		t.Fatalf("expected independent_parallel_jobs=true, got %#v", orchestration)
	}
}

func TestRAGCompressToolCall(t *testing.T) {
	server := newTestServer()
	initResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params:  mustRawJSON(t, `{"auth":{"actor":"alice","role":"user"}}`),
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}
	params := map[string]any{
		"name": "rag_compress",
		"arguments": map[string]any{
			"query": "why did the build fail?",
			"input_refs": []map[string]any{
				{"type": "log", "uri": "file:///tmp/build.log"},
			},
			"constraints": map[string]any{
				"retrieved_chunk_budget": 64000,
			},
		},
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramBytes,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(types.SubmitJobResponse)
	if structured.JobID == "" {
		t.Fatalf("expected job_id, got %#v", structured)
	}
}

func TestRAGCompressToolCallPreservesQuery(t *testing.T) {
	server := newTestServer()
	initResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params:  mustRawJSON(t, `{"auth":{"actor":"alice","role":"user"}}`),
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}
	callResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawJSON(t, `{
		  "name": "rag_compress",
		  "arguments": {
		    "query": "why did the build fail?",
		    "retrieval_strategies": ["ripgrep", "bm25"],
		    "input_refs": [{"type": "log", "uri": "file:///tmp/build.log"}]
		  }
		}`),
	})
	if callResp.Error != nil {
		t.Fatalf("unexpected error: %v", callResp.Error)
	}
	submit := callResp.Result.(map[string]any)["structuredContent"].(types.SubmitJobResponse)

	statusResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: mustRawJSON(t, fmt.Sprintf(`{
		  "name": "get_job_status",
		  "arguments": {"job_id": %q}
		}`, submit.JobID)),
	})
	if statusResp.Error != nil {
		t.Fatalf("unexpected status error: %v", statusResp.Error)
	}
	job := statusResp.Result.(map[string]any)["structuredContent"].(types.Job)
	if job.Request.TaskParams["query"] != "why did the build fail?" {
		t.Fatalf("expected normalized query task param, got %#v", job.Request.TaskParams)
	}
	if strategies, ok := job.Request.TaskParams["retrieval_strategies"].([]any); ok && len(strategies) == 2 {
	} else {
		t.Fatalf("expected normalized retrieval strategies, got %#v", job.Request.TaskParams)
	}
}

func TestGetJobStatusIncludesRuntimeDiagnostics(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		t.TempDir(),
	)
	server := NewServer(svc, mcpTestPrincipal())

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

	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawJSON(t, `{
		  "name": "get_job_status",
		  "arguments": {"job_id": "job_runtime_status"}
		}`),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected status error: %v", resp.Error)
	}
	got := resp.Result.(map[string]any)["structuredContent"].(types.Job)
	if got.RuntimeDiagnostics["backend_mode"] != "unavailable" {
		t.Fatalf("expected runtime diagnostics in get_job_status, got %#v", got.RuntimeDiagnostics)
	}
	if !got.DegradedLocalExecution || !got.RetryRecommended || got.ExecutionQuality != "no_real_backend" {
		t.Fatalf("expected summary flags in get_job_status, got %#v", got)
	}
}

func TestSubmitParallelJobsToolCall(t *testing.T) {
	server := newTestServer()
	initResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params:  mustRawJSON(t, `{"auth":{"actor":"alice","role":"user"}}`),
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}
	params := map[string]any{
		"name": "submit_parallel_jobs",
		"arguments": map[string]any{
			"task_type":     "document_summary",
			"output_schema": map[string]any{"name": "document_summary_v1"},
			"children": []map[string]any{
				{
					"input_refs":  []map[string]any{{"type": "file", "uri": "file:///tmp/a.txt"}},
					"shard_key":   "repo:a",
					"shard_index": 0,
					"shard_count": 2,
				},
				{
					"input_refs":  []map[string]any{{"type": "file", "uri": "file:///tmp/b.txt"}},
					"shard_key":   "repo:b",
					"shard_index": 1,
					"shard_count": 2,
				},
			},
			"reducer": map[string]any{
				"task_type":     "document_summary",
				"output_schema": map[string]any{"name": "document_summary_v1"},
			},
		},
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramBytes,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(types.SubmitParallelJobsResponse)
	if structured.RootJobID == "" {
		t.Fatalf("expected root_job_id, got %#v", structured)
	}
	if len(structured.Children) != 2 {
		t.Fatalf("expected 2 children, got %#v", structured)
	}
	if structured.ReducerJob == nil {
		t.Fatalf("expected reducer job, got %#v", structured)
	}
}

func TestGetRootJobStatusToolCall(t *testing.T) {
	server := newTestServer()
	initResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params:  mustRawJSON(t, `{"auth":{"actor":"alice","role":"user"}}`),
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}

	submitParams := mustRawJSON(t, `{
	  "name": "submit_parallel_jobs",
	  "arguments": {
	    "task_type": "document_summary",
	    "output_schema": {"name": "document_summary_v1"},
	    "children": [
	      {"input_refs": [{"type":"file","uri":"file:///tmp/a.txt"}], "shard_index": 0, "shard_count": 2},
	      {"input_refs": [{"type":"file","uri":"file:///tmp/b.txt"}], "shard_index": 1, "shard_count": 2}
	    ]
	  }
	}`)
	submitResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: submitParams,
	})
	if submitResp.Error != nil {
		t.Fatalf("submit error: %v", submitResp.Error)
	}
	submitStructured := submitResp.Result.(map[string]any)["structuredContent"].(types.SubmitParallelJobsResponse)

	call := map[string]any{
		"name": "get_root_job_status",
		"arguments": map[string]any{
			"root_job_id": submitStructured.RootJobID,
		},
	}
	paramBytes, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: paramBytes,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	structured := resp.Result.(map[string]any)["structuredContent"].(types.RootJobStatus)
	if structured.RootJobID != submitStructured.RootJobID {
		t.Fatalf("unexpected root status: %#v", structured)
	}
	if structured.TotalJobs != 2 {
		t.Fatalf("expected 2 total jobs, got %#v", structured)
	}
}

func TestRetryFailedRootShardsToolCall(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.NewWithAuditAndOptions(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		nil,
		runRoot,
		".",
		service.Options{RootActionMaxRetriedShards: 2},
	)
	now := time.Now().UTC()
	for _, job := range []types.Job{
		{
			ID: "job_ok", TaskType: "document_summary", State: types.JobStateSucceeded, RootJobID: "root_retry_tool",
			Request:       types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
			Orchestration: &types.OrchestrationInfo{RootJobID: "root_retry_tool", Strategy: "fanout_child", ShardKey: "doc:a", ShardIndex: 0, ShardCount: 2},
			CreatedAt:     now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute), SubmittedAt: now.Add(-2 * time.Minute),
		},
		{
			ID: "job_failed", TaskType: "document_summary", State: types.JobStateFailed, RootJobID: "root_retry_tool",
			Request:       types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
			Orchestration: &types.OrchestrationInfo{RootJobID: "root_retry_tool", Strategy: "fanout_child", ShardKey: "doc:b", ShardIndex: 1, ShardCount: 2},
			CreatedAt:     now.Add(-1 * time.Minute), UpdatedAt: now.Add(-1 * time.Minute), SubmittedAt: now.Add(-1 * time.Minute),
		},
	} {
		if err := jobStore.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	server := NewServer(svc, mcpTestPrincipal())

	params := mustRawJSON(t, `{
	  "name": "retry_failed_root_shards",
	  "arguments": {
	    "root_job_id": "root_retry_tool"
	  }
	}`)
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	structured := resp.Result.(map[string]any)["structuredContent"].(types.RetryFailedRootShardsResponse)
	if structured.RetriedCount != 1 {
		t.Fatalf("expected one retried shard, got %#v", structured)
	}
	if structured.CumulativeRetriedShards != 1 || structured.RemainingRetriedShardBudget != 1 {
		t.Fatalf("expected direct retry budget counters, got %#v", structured)
	}
}

func TestReleaseDeferredRootChunksToolCall(t *testing.T) {
	svc := newMCPThrottledBatchService(t, 2)
	server := NewServer(svc, mcpTestPrincipal())

	submitResp, err := submitParallelJobsAsMCPUser(svc, types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     throttledFourChildRequests(),
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}

	params := mustRawJSON(t, fmt.Sprintf(`{
	  "name": "release_deferred_root_chunks",
	  "arguments": {
	    "root_job_id": %q,
	    "max_additional_batches": 1
	  }
	}`, submitResp.RootJobID))
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	structured := resp.Result.(map[string]any)["structuredContent"].(types.ReleaseDeferredRootChunksResponse)
	if structured.ReleasedChunks != 1 || structured.ReleasedChildren != 2 {
		t.Fatalf("expected one released chunk with two children, got %#v", structured)
	}
	if structured.CumulativeForcedReleaseChunks != 1 || structured.RemainingForcedReleaseBudget != 1 {
		t.Fatalf("expected direct forced-release budget counters, got %#v", structured)
	}
}

func TestRetryFailedRootShardsToolCallReturnsErrorWhenCumulativeBudgetExceeded(t *testing.T) {
	server, rootJobID := newMCPRetryBudgetExceededFixture(t)
	second := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: mustRawJSON(t, fmt.Sprintf(`{
		  "name": "retry_failed_root_shards",
		  "arguments": {"root_job_id": %q}
		}`, rootJobID)),
	})
	if second.Error == nil {
		t.Fatal("expected cumulative retry budget error")
	}
	assertToolErrorMessage(t, second.Error, mcpRetryBudgetExceededMessage)
}

func TestReleaseDeferredRootChunksToolCallReturnsErrorWhenCumulativeBudgetExceeded(t *testing.T) {
	server, rootJobID := newMCPReleaseBudgetExceededFixture(t)
	second := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: mustRawJSON(t, fmt.Sprintf(`{
		  "name": "release_deferred_root_chunks",
		  "arguments": {"root_job_id": %q, "max_additional_batches": 1}
		}`, rootJobID)),
	})
	if second.Error == nil {
		t.Fatal("expected cumulative forced-release budget error")
	}
	assertToolErrorMessage(t, second.Error, mcpReleaseBudgetExceededMessage)
}

func TestFetchJobLogsToolCall(t *testing.T) {
	runRoot := t.TempDir()
	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	server := NewServer(svc, mcpTestPrincipal())

	submitResp, err := submitJobAsMCPUser(svc, types.SubmitJobRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}
	jobDir := filepath.Join(runRoot, submitResp.JobID)
	if err := os.WriteFile(filepath.Join(jobDir, "stdout.log"), []byte("token=abc123\n"), 0o644); err != nil {
		t.Fatalf("write stdout: %v", err)
	}

	params := map[string]any{
		"name": "fetch_job_logs",
		"arguments": map[string]any{
			"job_id": submitResp.JobID,
			"stream": "stdout",
		},
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramBytes,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(types.JobLogs)
	if strings.Contains(structured.Content, "abc123") {
		t.Fatalf("expected redacted log content, got %q", structured.Content)
	}
}

func TestFetchJobLogsToolCallPolicyDenied(t *testing.T) {
	runRoot := t.TempDir()
	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	server := NewServer(svc, mcpTestPrincipal())

	submitResp, err := submitJobAsMCPUser(svc, types.SubmitJobRequest{
		TaskType: "log_analysis",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file:///tmp/build.log", Classification: "phi"},
		},
		OutputSchema: types.OutputSchemaRef{Name: "log_analysis_v1"},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	params := map[string]any{
		"name": "fetch_job_logs",
		"arguments": map[string]any{
			"job_id": submitResp.JobID,
		},
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramBytes,
	})
	if resp.Error == nil {
		t.Fatal("expected policy denial error")
	}
	if !strings.Contains(resp.Error.Message, "policy denied") {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
}

func TestFetchResultToolCallAppliesReleasePolicy(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	server := NewServer(svc, mcpTestPrincipal())

	now := time.Now().UTC()
	job := types.Job{
		ID:       "job_mcp_result_policy",
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

	params := map[string]any{
		"name": "fetch_result",
		"arguments": map[string]any{
			"job_id": job.ID,
		},
	}
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramBytes,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(types.JobResultRelease)
	if structured.Result == nil {
		t.Fatal("expected released result")
	}
	entrypoints := structured.Result.Payload["entrypoints"].([]any)
	first := entrypoints[0].(map[string]any)
	if first["path"] != "[REDACTED]" {
		t.Fatalf("expected redacted path, got %#v", first["path"])
	}
	if len(structured.Artifacts) != 0 {
		t.Fatalf("expected withheld artifacts, got %#v", structured.Artifacts)
	}
	if structured.RuntimeDiagnostics["backend_mode"] != "unavailable" {
		t.Fatalf("expected runtime diagnostics in structured release, got %#v", structured.RuntimeDiagnostics)
	}
	if !structured.DegradedLocalExecution || !structured.RetryRecommended || structured.ExecutionQuality != "no_real_backend" {
		t.Fatalf("expected summary flags in structured release, got %#v", structured)
	}
}

func TestRetryRecommendationTools(t *testing.T) {
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.New(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		runRoot,
		".",
	)
	server := NewServer(svc, mcpTestPrincipal())

	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_mcp_retry_rec",
		TaskType:    "rag_compress",
		State:       types.JobStateSucceeded,
		SubmittedBy: "mcp:test",
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

	recResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawJSON(t, `{
		  "name": "get_retry_recommendation",
		  "arguments": {"job_id": "job_mcp_retry_rec"}
		}`),
	})
	if recResp.Error != nil {
		t.Fatalf("unexpected recommendation error: %#v", recResp.Error)
	}
	rec := recResp.Result.(map[string]any)["structuredContent"].(types.JobRetryRecommendation)
	if rec.ExecutionProfile.Tier != "a100-reasoning" {
		t.Fatalf("expected a100 retry recommendation, got %#v", rec)
	}
	if rec.PlacementHint.TierPreference != "a100-reasoning" || !rec.PlacementHint.Preemptible {
		t.Fatalf("expected placement hint on recommendation, got %#v", rec)
	}

	retryResp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: mustRawJSON(t, `{
		  "name": "retry_with_recommended_profile",
		  "arguments": {"job_id": "job_mcp_retry_rec"}
		}`),
	})
	if retryResp.Error != nil {
		t.Fatalf("unexpected retry error: %#v", retryResp.Error)
	}
	submit := retryResp.Result.(map[string]any)["structuredContent"].(types.SubmitJobResponse)
	retriedJob, err := jobStore.GetJob(context.Background(), submit.JobID)
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

func newTestServer() *Server {
	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		".broker/runs",
		".",
	)
	return NewServer(svc, mcpTestPrincipal())
}

func submitJobAsMCPUser(svc *service.Service, req types.SubmitJobRequest) (*types.SubmitJobResponse, error) {
	resp, err := svc.SubmitJob(auth.WithPrincipal(context.Background(), mcpTestPrincipal()), req)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func submitParallelJobsAsMCPUser(svc *service.Service, req types.SubmitParallelJobsRequest) (*types.SubmitParallelJobsResponse, error) {
	resp, err := svc.SubmitParallelJobs(auth.WithPrincipal(context.Background(), mcpTestPrincipal()), req)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func newMCPThrottledBatchService(t *testing.T, releaseBudget int) *service.Service {
	t.Helper()
	return service.NewWithAuditAndOptions(
		store.NewMemoryJobStore(),
		&mcpTestBatchBackend{status: backends.RunStatus{State: types.JobStateQueued, RawState: "PENDING"}},
		log.New(io.Discard, "", 0),
		nil,
		t.TempDir(),
		".",
		service.Options{
			ParallelMaxBatchSize:           2,
			ParallelMaxActiveBatches:       1,
			RootActionMaxAdditionalBatches: releaseBudget,
		},
	)
}

func assertToolErrorMessage(t *testing.T, err *respError, expectedMessage string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected tool error")
	}
	if err.Code != -32000 || !strings.Contains(err.Message, expectedMessage) {
		t.Fatalf("unexpected error: %#v expected_substring=%q", err, expectedMessage)
	}
}

func newMCPRetryBudgetExceededFixture(t *testing.T) (*Server, string) {
	t.Helper()
	runRoot := t.TempDir()
	jobStore := store.NewMemoryJobStore()
	svc := service.NewWithAuditAndOptions(
		jobStore,
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		nil,
		runRoot,
		".",
		service.Options{RootActionMaxRetriedShards: 1},
	)
	now := time.Now().UTC()
	rootJobID := "root_retry_tool_cap"
	job := types.Job{
		ID: "job_failed_once", TaskType: "document_summary", State: types.JobStateFailed, RootJobID: rootJobID,
		SubmittedBy: "mcp:test",
		Request:     types.SubmitJobRequest{TaskType: "document_summary", OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"}},
		Orchestration: &types.OrchestrationInfo{
			RootJobID: rootJobID, Strategy: "fanout_child", ShardIndex: 0, ShardCount: 1,
		},
		CreatedAt: now, UpdatedAt: now, SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	server := NewServer(svc, mcpTestPrincipal())
	first := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: mustRawJSON(t, fmt.Sprintf(`{
		  "name": "retry_failed_root_shards",
		  "arguments": {"root_job_id": %q}
		}`, rootJobID)),
	})
	if first.Error != nil {
		t.Fatalf("unexpected first retry error: %#v", first.Error)
	}
	firstStructured := first.Result.(map[string]any)["structuredContent"].(types.RetryFailedRootShardsResponse)
	retriedJob, err := jobStore.GetJob(context.Background(), firstStructured.RetriedShards[0].JobID)
	if err != nil {
		t.Fatalf("get retried job: %v", err)
	}
	retriedJob.State = types.JobStateFailed
	retriedJob.BackendRunID = ""
	if err := jobStore.UpdateJob(context.Background(), retriedJob); err != nil {
		t.Fatalf("mark retried job failed again: %v", err)
	}
	return server, rootJobID
}

func newMCPReleaseBudgetExceededFixture(t *testing.T) (*Server, string) {
	t.Helper()
	svc := newMCPThrottledBatchService(t, 1)
	server := NewServer(svc, mcpTestPrincipal())
	submitResp, err := submitParallelJobsAsMCPUser(svc, types.SubmitParallelJobsRequest{
		TaskType:     "document_summary",
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		Children:     throttledSixChildRequests(),
	})
	if err != nil {
		t.Fatalf("submit parallel jobs: %v", err)
	}
	first := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: mustRawJSON(t, fmt.Sprintf(`{
		  "name": "release_deferred_root_chunks",
		  "arguments": {"root_job_id": %q, "max_additional_batches": 1}
		}`, submitResp.RootJobID)),
	})
	if first.Error != nil {
		t.Fatalf("unexpected first release error: %#v", first.Error)
	}
	return server, submitResp.RootJobID
}

func TestToolsCallRequiresInitializedIdentity(t *testing.T) {
	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		t.TempDir(),
		".",
	)
	server := NewServer(svc, auth.Principal{})
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	if resp.Error == nil {
		t.Fatal("expected identity error")
	}
	if !strings.Contains(resp.Error.Message, "identity") {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
}

func TestInitializeSetsSessionPrincipal(t *testing.T) {
	svc := service.New(
		store.NewMemoryJobStore(),
		slurm.NewBackend(config.Config{}),
		log.New(io.Discard, "", 0),
		t.TempDir(),
		".",
	)
	server := NewServer(svc, auth.Principal{})
	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  mustRawJSON(t, `{"auth":{"actor":"alice","role":"admin"}}`),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected initialize error: %#v", resp.Error)
	}
	server.mu.RLock()
	principal := server.sessionPrincipal
	server.mu.RUnlock()
	if principal.Actor != "alice" || principal.Role != "admin" {
		t.Fatalf("unexpected session principal: %#v", principal)
	}
}

func mustRawJSON(t *testing.T, text string) json.RawMessage {
	t.Helper()
	return json.RawMessage(text)
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

func frameJSON(payload string) string {
	return "Content-Length: " + fmt.Sprintf("%d", len(payload)) + "\r\n\r\n" + payload
}

func decodeFramedResponses(t *testing.T, payload []byte) [][]byte {
	t.Helper()
	reader := bytes.NewReader(payload)
	bufReader := io.Reader(reader)
	data, err := io.ReadAll(bufReader)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}

	remaining := string(data)
	var out [][]byte
	for len(strings.TrimSpace(remaining)) > 0 {
		parts := strings.SplitN(remaining, "\r\n\r\n", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid framed payload: %q", remaining)
		}
		header := parts[0]
		bodyAndRest := parts[1]

		var contentLength int
		if _, err := fmt.Sscanf(header, "Content-Length: %d", &contentLength); err != nil {
			t.Fatalf("parse content length: %v", err)
		}
		if len(bodyAndRest) < contentLength {
			t.Fatalf("body shorter than content length")
		}
		body := bodyAndRest[:contentLength]
		out = append(out, []byte(body))
		remaining = bodyAndRest[contentLength:]
	}
	return out
}

type mcpTestBatchBackend struct {
	status backends.RunStatus
}

func (f *mcpTestBatchBackend) Name() string { return "mcp-fake-batch" }

func (f *mcpTestBatchBackend) SubmitRun(context.Context, types.Job) (backends.SubmitResponse, error) {
	return backends.SubmitResponse{
		BackendKind:  "mcp-fake-batch",
		BackendRunID: "single-run-1",
		InitialState: types.JobStateQueued,
	}, nil
}

func (f *mcpTestBatchBackend) SubmitRunBatch(_ context.Context, jobs []types.Job) ([]backends.SubmitResponse, error) {
	responses := make([]backends.SubmitResponse, 0, len(jobs))
	for i := range jobs {
		responses = append(responses, backends.SubmitResponse{
			BackendKind:  "mcp-fake-batch",
			BackendRunID: "batch-run-" + string(rune('0'+i)),
			InitialState: types.JobStateQueued,
		})
	}
	return responses, nil
}

func (f *mcpTestBatchBackend) GetRun(context.Context, string) (backends.RunStatus, error) {
	return f.status, nil
}

func (f *mcpTestBatchBackend) CancelRun(context.Context, string) error { return nil }

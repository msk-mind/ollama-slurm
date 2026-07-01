package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func TestKeyForRequestFileTasks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	key, cacheable, err := KeyForRequest(types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + path},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
	})
	if err != nil {
		t.Fatalf("key for request: %v", err)
	}
	if !cacheable {
		t.Fatal("expected cacheable request")
	}
	if key == "" {
		t.Fatal("expected non-empty key")
	}
}

func TestFindCompletedJobByCacheKey(t *testing.T) {
	jobStore := store.NewMemoryJobStore()
	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_1",
		State:       types.JobStateSucceeded,
		CacheKey:    "sha256:test",
		Result:      &types.Result{SchemaName: "document_summary_v1", SchemaVersion: "1.0.0", Payload: map[string]any{"summary": "ok"}},
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := jobStore.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	found, err := FindCompletedJobByCacheKey(context.Background(), jobStore, "sha256:test")
	if err != nil {
		t.Fatalf("find by cache key: %v", err)
	}
	if found == nil || found.ID != "job_1" {
		t.Fatalf("expected job_1, got %#v", found)
	}
}

func TestKeyForRequestDirectoryTasks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "broker"), 0o755); err != nil {
		t.Fatalf("mkdir broker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broker", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	key, cacheable, err := KeyForRequest(types.SubmitJobRequest{
		TaskType: "repo_summary",
		InputRefs: []types.InputRef{
			{Type: "directory", URI: "file://" + dir},
		},
		OutputSchema: types.OutputSchemaRef{Name: "repo_summary_v1"},
	})
	if err != nil {
		t.Fatalf("key for request: %v", err)
	}
	if !cacheable {
		t.Fatal("expected repo_summary request to be cacheable")
	}
	if key == "" {
		t.Fatal("expected non-empty key")
	}
}

func TestKeyForRequestChangesWhenExecutionProfileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	baseReq := types.SubmitJobRequest{
		TaskType: "document_summary",
		InputRefs: []types.InputRef{
			{Type: "file", URI: "file://" + path},
		},
		OutputSchema: types.OutputSchemaRef{Name: "document_summary_v1"},
		ExecutionProfile: types.ExecutionProfile{
			Tier:    "p40-rag-compression",
			Model:   "gpt-oss-20b.p40",
			Runtime: "llama.cpp",
		},
	}
	keyA, cacheable, err := KeyForRequest(baseReq)
	if err != nil {
		t.Fatalf("key for request A: %v", err)
	}
	if !cacheable {
		t.Fatal("expected cacheable request")
	}

	baseReq.ExecutionProfile.Model = "qwen3-coder-30b.a100"
	keyB, cacheable, err := KeyForRequest(baseReq)
	if err != nil {
		t.Fatalf("key for request B: %v", err)
	}
	if !cacheable {
		t.Fatal("expected cacheable request")
	}
	if keyA == keyB {
		t.Fatalf("expected cache key to change when model changes, got %q", keyA)
	}
}

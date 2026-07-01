package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func TestFileJobStorePersistsJobs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	store, err := NewFileJobStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	now := time.Now().UTC()
	job := types.Job{
		ID:          "job_test",
		TaskType:    "document_summary",
		State:       types.JobStateQueued,
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	if err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	reloaded, err := NewFileJobStore(path)
	if err != nil {
		t.Fatalf("reload file store: %v", err)
	}

	got, err := reloaded.GetJob(context.Background(), "job_test")
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.ID != job.ID {
		t.Fatalf("expected %q, got %q", job.ID, got.ID)
	}
}

func TestFileJobStoreMergesWritesAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	storeA, err := NewFileJobStore(path)
	if err != nil {
		t.Fatalf("new file store A: %v", err)
	}
	storeB, err := NewFileJobStore(path)
	if err != nil {
		t.Fatalf("new file store B: %v", err)
	}

	now := time.Now().UTC()
	jobA := types.Job{
		ID:          "job_a",
		TaskType:    "document_summary",
		State:       types.JobStateQueued,
		CreatedAt:   now,
		UpdatedAt:   now,
		SubmittedAt: now,
	}
	jobB := types.Job{
		ID:          "job_b",
		TaskType:    "log_analysis",
		State:       types.JobStateQueued,
		CreatedAt:   now.Add(time.Second),
		UpdatedAt:   now.Add(time.Second),
		SubmittedAt: now.Add(time.Second),
	}

	if err := storeA.CreateJob(context.Background(), jobA); err != nil {
		t.Fatalf("create job A: %v", err)
	}
	if err := storeB.CreateJob(context.Background(), jobB); err != nil {
		t.Fatalf("create job B: %v", err)
	}

	reloaded, err := NewFileJobStore(path)
	if err != nil {
		t.Fatalf("reload file store: %v", err)
	}
	jobs, err := reloaded.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].ID != "job_a" || jobs[1].ID != "job_b" {
		t.Fatalf("expected deterministic ordering, got %#v", jobs)
	}
}

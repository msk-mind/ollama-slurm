package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/types"
)

var ErrNotFound = errors.New("job not found")

type JobStore interface {
	CreateJob(context.Context, types.Job) error
	GetJob(context.Context, string) (types.Job, error)
	UpdateJob(context.Context, types.Job) error
	ListJobs(context.Context) ([]types.Job, error)
}

type MemoryJobStore struct {
	mu   sync.RWMutex
	jobs map[string]types.Job
}

func NewMemoryJobStore() *MemoryJobStore {
	return &MemoryJobStore{
		jobs: make(map[string]types.Job),
	}
}

func (s *MemoryJobStore) CreateJob(_ context.Context, job types.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.ID]; exists {
		return errors.New("job already exists")
	}
	s.jobs[job.ID] = job
	return nil
}

func (s *MemoryJobStore) GetJob(_ context.Context, id string) (types.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return types.Job{}, ErrNotFound
	}
	return job, nil
}

func (s *MemoryJobStore) UpdateJob(_ context.Context, job types.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.ID]; !exists {
		return ErrNotFound
	}
	job.UpdatedAt = time.Now().UTC()
	s.jobs[job.ID] = job
	return nil
}

func (s *MemoryJobStore) ListJobs(_ context.Context) ([]types.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]types.Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	return jobs, nil
}

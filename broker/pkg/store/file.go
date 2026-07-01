package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"

	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type FileJobStore struct {
	mu       sync.RWMutex
	path     string
	lockPath string
	jobs     map[string]types.Job
}

func NewFileJobStore(path string) (*FileJobStore, error) {
	store := &FileJobStore{
		path:     path,
		lockPath: path + ".lock",
		jobs:     make(map[string]types.Job),
	}
	if err := store.withFileLock(func() error {
		return store.loadLocked()
	}); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileJobStore) CreateJob(_ context.Context, job types.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withFileLock(func() error {
		if err := s.loadLocked(); err != nil {
			return err
		}
		if _, exists := s.jobs[job.ID]; exists {
			return errors.New("job already exists")
		}
		s.jobs[job.ID] = job
		return s.persistLocked()
	})
}

func (s *FileJobStore) GetJob(_ context.Context, id string) (types.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.withFileLock(func() error {
		return s.loadLocked()
	}); err != nil {
		return types.Job{}, err
	}
	job, ok := s.jobs[id]
	if !ok {
		return types.Job{}, ErrNotFound
	}
	return job, nil
}

func (s *FileJobStore) UpdateJob(_ context.Context, job types.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withFileLock(func() error {
		if err := s.loadLocked(); err != nil {
			return err
		}
		if _, exists := s.jobs[job.ID]; !exists {
			return ErrNotFound
		}
		s.jobs[job.ID] = job
		return s.persistLocked()
	})
}

func (s *FileJobStore) ListJobs(_ context.Context) ([]types.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.withFileLock(func() error {
		return s.loadLocked()
	}); err != nil {
		return nil, err
	}
	jobs := make([]types.Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].SubmittedAt.Equal(jobs[j].SubmittedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].SubmittedAt.Before(jobs[j].SubmittedAt)
	})
	return jobs, nil
}

func (s *FileJobStore) loadLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.jobs = make(map[string]types.Job)
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		s.jobs = make(map[string]types.Job)
		return nil
	}
	loaded := make(map[string]types.Job)
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	s.jobs = loaded
	return nil
}

func (s *FileJobStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(s.path), "jobs-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func (s *FileJobStore) withFileLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.lockPath), 0o755); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	return fn()
}

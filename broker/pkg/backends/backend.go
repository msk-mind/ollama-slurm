package backends

import (
	"context"

	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type Backend interface {
	Name() string
	SubmitRun(context.Context, types.Job) (SubmitResponse, error)
	GetRun(context.Context, string) (RunStatus, error)
	CancelRun(context.Context, string) error
}

type BatchBackend interface {
	SubmitRunBatch(context.Context, []types.Job) ([]SubmitResponse, error)
}

type SubmitResponse struct {
	BackendKind  string
	BackendRunID string
	InitialState types.JobState
}

type RunStatus struct {
	BackendRunID string
	State        types.JobState
	RawState     string
	ExitCode     string
}

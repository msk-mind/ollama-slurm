package authz

import (
	"errors"
	"fmt"

	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

var ErrForbidden = errors.New("forbidden")

func AuthorizeJobAccess(principal auth.Principal, job types.Job) error {
	if auth.IsAdmin(principal) {
		return nil
	}
	if job.SubmittedBy == "" {
		return nil
	}
	if principal.Actor == "" {
		return fmt.Errorf("%w: missing actor identity", ErrForbidden)
	}
	if principal.Actor != job.SubmittedBy {
		return fmt.Errorf("%w: actor %q cannot access job owned by %q", ErrForbidden, principal.Actor, job.SubmittedBy)
	}
	return nil
}

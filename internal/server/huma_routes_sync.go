package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	ghclient "github.com/wesm/middleman/internal/github"
)

// --- inputs ------------------------------------------------------------------

type syncRepoInput struct {
	Owner string `path:"owner"`
	Name  string `path:"name"`
}

// --- handlers ----------------------------------------------------------------

// triggerSync kicks off a full sync over every configured repo.
// Identical to the previous implementation in huma_routes.go; moved
// here to colocate sync handlers.
func (s *Server) triggerSync(
	ctx context.Context, _ *struct{},
) (*acceptedOutput, error) {
	s.syncer.TriggerRun(context.WithoutCancel(ctx))
	return &acceptedOutput{Status: http.StatusAccepted}, nil
}

// syncRepo kicks off an ad-hoc sync limited to one repo. The repo
// must already be in the configured list; untracked repos return 403
// to match the convention used by syncPR. The host is resolved via
// Syncer.HostForRepo (which defaults unknown owner/name pairs to
// github.com) before the tracked-set check.
func (s *Server) syncRepo(
	ctx context.Context, input *syncRepoInput,
) (*acceptedOutput, error) {
	host := s.syncer.HostForRepo(input.Owner, input.Name)
	if !s.syncer.IsTrackedRepoOnHost(input.Owner, input.Name, host) {
		return nil, huma.Error403Forbidden(
			"repo " + input.Owner + "/" + input.Name +
				" on " + host + " is not tracked",
		)
	}
	if err := s.syncer.TriggerRunForRepos(
		context.WithoutCancel(ctx),
		[]ghclient.RepoRef{{
			Owner:        input.Owner,
			Name:         input.Name,
			PlatformHost: host,
		}},
	); err != nil {
		return nil, huma.Error500InternalServerError(
			"trigger repo sync: " + err.Error(),
		)
	}
	return &acceptedOutput{Status: http.StatusAccepted}, nil
}

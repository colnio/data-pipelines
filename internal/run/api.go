package run

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/platform"
)

// Register wires the run browse/read endpoints onto the huma API (architecture
// §19). These are human-facing and require an authenticated principal with the
// catalog read scope.
func Register(api huma.API, r *Repo) {
	huma.Register(api, huma.Operation{
		OperationID: "runs-list",
		Method:      http.MethodGet,
		Path:        "/v1/runs",
		Summary:     "List runs",
		Description: "Browse/search runs by state, sample, device, measurement type, or agent. Newest first.",
		Tags:        []string{"runs"},
	}, r.handleList)

	huma.Register(api, huma.Operation{
		OperationID: "runs-get",
		Method:      http.MethodGet,
		Path:        "/v1/runs/{id}",
		Summary:     "Get a run",
		Description: "Returns a run with its verified raw files and full state-transition audit trail.",
		Tags:        []string{"runs"},
	}, r.handleGet)
}

type listRunsInput struct {
	State           string `query:"state"`
	SampleID        string `query:"sample_id"`
	DeviceID        string `query:"device_id"`
	MeasurementType string `query:"measurement_type"`
	AgentID         string `query:"agent_id"`
	Limit           int    `query:"limit"`
}

type listRunsOutput struct {
	Body struct {
		Runs []domain.Run `json:"runs"`
	}
}

func (r *Repo) handleList(ctx context.Context, in *listRunsInput) (*listRunsOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}
	runs, err := r.List(ctx, ListFilter{
		State: in.State, SampleID: in.SampleID, DeviceID: in.DeviceID,
		MeasurementType: in.MeasurementType, AgentID: in.AgentID, Limit: in.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := &listRunsOutput{}
	out.Body.Runs = runs
	return out, nil
}

type getRunInput struct {
	ID string `path:"id"`
}

type getRunOutput struct {
	Body struct {
		Run         domain.Run                  `json:"run"`
		Files       []domain.RunFile            `json:"files"`
		Transitions []domain.RunStateTransition `json:"transitions"`
	}
}

func (r *Repo) handleGet(ctx context.Context, in *getRunInput) (*getRunOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadRuns); err != nil {
		return nil, err
	}
	run, err := r.Get(ctx, in.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, platform.NotFound("run.not_found", "run does not exist")
	}
	if err != nil {
		return nil, err
	}
	files, err := r.Files(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	transitions, err := r.Transitions(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := &getRunOutput{}
	out.Body.Run = run
	out.Body.Files = files
	out.Body.Transitions = transitions
	return out, nil
}

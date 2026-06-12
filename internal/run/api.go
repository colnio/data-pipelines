package run

import (
	"context"
	"errors"
	"net/http"
	"time"

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
	// Existing filters.
	State           string `query:"state"`
	SampleID        string `query:"sample_id"`
	DeviceID        string `query:"device_id"`
	MeasurementType string `query:"measurement_type"`
	AgentID         string `query:"agent_id"`
	Limit           int    `query:"limit"`

	// New filters.
	ConditionLabel    string `query:"condition_label"`
	DeclaredAfter     string `query:"declared_after"`  // RFC3339; absent when empty
	DeclaredBefore    string `query:"declared_before"` // RFC3339; absent when empty
	PublicationStatus string `query:"publication_status"` // "published"|"unpublished"|""
	Cursor            string `query:"cursor"`
}

type listRunsOutput struct {
	Body struct {
		Runs       []domain.Run `json:"runs"`
		NextCursor string       `json:"next_cursor,omitempty"`
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

	var declaredAfter, declaredBefore time.Time
	if in.DeclaredAfter != "" {
		t, err := time.Parse(time.RFC3339, in.DeclaredAfter)
		if err != nil {
			return nil, platform.BadRequest("run.invalid_declared_after", "declared_after must be RFC3339")
		}
		declaredAfter = t
	}
	if in.DeclaredBefore != "" {
		t, err := time.Parse(time.RFC3339, in.DeclaredBefore)
		if err != nil {
			return nil, platform.BadRequest("run.invalid_declared_before", "declared_before must be RFC3339")
		}
		declaredBefore = t
	}

	runs, nextCursor, err := r.List(ctx, ListFilter{
		State: in.State, SampleID: in.SampleID, DeviceID: in.DeviceID,
		MeasurementType: in.MeasurementType, AgentID: in.AgentID, Limit: in.Limit,
		ConditionLabel:   in.ConditionLabel,
		DeclaredAfter:    declaredAfter,
		DeclaredBefore:   declaredBefore,
		PublicationStatus: in.PublicationStatus,
		Cursor:           in.Cursor,
	})
	if err != nil {
		return nil, err
	}
	out := &listRunsOutput{}
	out.Body.Runs = runs
	out.Body.NextCursor = nextCursor
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

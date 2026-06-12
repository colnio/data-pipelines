package catalog

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/platform"
)

// ── Samples ───────────────────────────────────────────────────────────────────

func registerSamples(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "catalog-list-samples",
		Method:      http.MethodGet,
		Path:        "/v1/samples",
		Summary:     "List samples",
		Description: "Browse samples from the catalog, paginated by created_at keyset.",
		Tags:        []string{"catalog"},
	}, svc.handleListSamples)

	huma.Register(api, huma.Operation{
		OperationID: "catalog-get-sample",
		Method:      http.MethodGet,
		Path:        "/v1/samples/{id}",
		Summary:     "Get a sample",
		Description: "Returns a single sample by id.",
		Tags:        []string{"catalog"},
	}, svc.handleGetSample)
}

type catalogListSamplesInput struct {
	Limit  int    `query:"limit"`
	Cursor string `query:"cursor"`
}

type catalogListSamplesOutput struct {
	Body struct {
		Samples    []domain.Sample `json:"samples"`
		NextCursor string          `json:"next_cursor,omitempty"`
	}
}

func (s *Service) handleListSamples(ctx context.Context, in *catalogListSamplesInput) (*catalogListSamplesOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}
	samples, next, err := s.ListSamples(ctx, in.Limit, in.Cursor)
	if err != nil {
		return nil, err
	}
	out := &catalogListSamplesOutput{}
	out.Body.Samples = samples
	out.Body.NextCursor = next
	return out, nil
}

type catalogGetSampleInput struct {
	ID string `path:"id"`
}

type catalogGetSampleOutput struct {
	Body struct {
		Sample domain.Sample `json:"sample"`
	}
}

func (s *Service) handleGetSample(ctx context.Context, in *catalogGetSampleInput) (*catalogGetSampleOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}
	sample, err := s.GetSample(ctx, in.ID)
	if err != nil {
		return nil, mapCatalogErr(err)
	}
	out := &catalogGetSampleOutput{}
	out.Body.Sample = sample
	return out, nil
}

// ── Devices ───────────────────────────────────────────────────────────────────

func registerDevices(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "catalog-list-devices",
		Method:      http.MethodGet,
		Path:        "/v1/devices",
		Summary:     "List devices",
		Description: "Browse devices, optionally filtered by sample_id, paginated by created_at keyset.",
		Tags:        []string{"catalog"},
	}, svc.handleListDevices)

	huma.Register(api, huma.Operation{
		OperationID: "catalog-get-device",
		Method:      http.MethodGet,
		Path:        "/v1/devices/{id}",
		Summary:     "Get a device",
		Description: "Returns a single device by id.",
		Tags:        []string{"catalog"},
	}, svc.handleGetDevice)
}

type catalogListDevicesInput struct {
	SampleID string `query:"sample_id"`
	Limit    int    `query:"limit"`
	Cursor   string `query:"cursor"`
}

type catalogListDevicesOutput struct {
	Body struct {
		Devices    []domain.Device `json:"devices"`
		NextCursor string          `json:"next_cursor,omitempty"`
	}
}

func (s *Service) handleListDevices(ctx context.Context, in *catalogListDevicesInput) (*catalogListDevicesOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}
	devices, next, err := s.ListDevices(ctx, in.SampleID, in.Limit, in.Cursor)
	if err != nil {
		return nil, err
	}
	out := &catalogListDevicesOutput{}
	out.Body.Devices = devices
	out.Body.NextCursor = next
	return out, nil
}

type catalogGetDeviceInput struct {
	ID string `path:"id"`
}

type catalogGetDeviceOutput struct {
	Body struct {
		Device domain.Device `json:"device"`
	}
}

func (s *Service) handleGetDevice(ctx context.Context, in *catalogGetDeviceInput) (*catalogGetDeviceOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}
	device, err := s.GetDevice(ctx, in.ID)
	if err != nil {
		return nil, mapCatalogErr(err)
	}
	out := &catalogGetDeviceOutput{}
	out.Body.Device = device
	return out, nil
}

// ── ContactConfigs ────────────────────────────────────────────────────────────

func registerContactConfigs(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "catalog-list-contact-configs",
		Method:      http.MethodGet,
		Path:        "/v1/contact-configs",
		Summary:     "List contact configurations",
		Description: "Browse contact configurations, optionally filtered by device_id, paginated by created_at keyset.",
		Tags:        []string{"catalog"},
	}, svc.handleListContactConfigs)
}

type catalogListContactConfigsInput struct {
	DeviceID string `query:"device_id"`
	Limit    int    `query:"limit"`
	Cursor   string `query:"cursor"`
}

type catalogListContactConfigsOutput struct {
	Body struct {
		ContactConfigs []domain.ContactConfig `json:"contact_configs"`
		NextCursor     string                 `json:"next_cursor,omitempty"`
	}
}

func (s *Service) handleListContactConfigs(ctx context.Context, in *catalogListContactConfigsInput) (*catalogListContactConfigsOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}
	configs, next, err := s.ListContactConfigs(ctx, in.DeviceID, in.Limit, in.Cursor)
	if err != nil {
		return nil, err
	}
	out := &catalogListContactConfigsOutput{}
	out.Body.ContactConfigs = configs
	out.Body.NextCursor = next
	return out, nil
}

// ── error mapping ─────────────────────────────────────────────────────────────

func mapCatalogErr(err error) error {
	var nf *notFoundErr
	if errors.As(err, &nf) {
		return platform.NotFound(nf.kind+".not_found", nf.Error())
	}
	return err
}

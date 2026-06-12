package admin

import "github.com/danielgtaylor/huma/v2"

// Register wires all admin endpoints onto the huma API.
func Register(api huma.API, svc *Service) {
	registerUserHandlers(api, svc)
	registerAgentHandlers(api, svc)
	registerInspectionHandlers(api, svc)
}

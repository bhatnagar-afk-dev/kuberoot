// Package app contains the services bound to the frontend via Wails.
//
// Services here form the Go<->UI contract: keep them thin, validate input,
// and delegate real work to packages under internal/. TypeScript bindings
// are generated from these types (see `wails3 generate bindings`).
package app

import "kuberoot.in/kuberoot/internal/version"

// SystemService exposes application-level information to the UI.
type SystemService struct{}

// Version returns build metadata for the running binary.
func (s *SystemService) Version() version.Info {
	return version.Get()
}

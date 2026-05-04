// Package api implements the HTTP REST and WebSocket handlers for the
// frontend. Frame format and verbs are specified in DESIGN.md §11.
//
// The HTTP side handles circuit CRUD and library operations; the
// WebSocket side handles streaming simulation results and cancellations.
//
// Handlers depend on the engine.Engine interface (not a concrete type)
// so unit tests can use an in-memory fake engine.
package api

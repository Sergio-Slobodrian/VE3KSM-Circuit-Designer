// Package circuit holds the in-memory data model that every other package
// renders against: the schematic editor, the netlist parser/emitter, the
// simulation engine adapter, and the HTTP/WS API.
//
// The canonical types (Circuit, Component, Wire, Probe, Source, Analysis,
// LibraryRef, Param, Layout) and their JSON tags are specified in
// DESIGN.md §4 — the frontend depends on those tags verbatim.
//
// This package is the spine of the application. Implement it first
// (milestone 1, see KICKOFF.md) and pause for review before building
// anything on top.
package circuit

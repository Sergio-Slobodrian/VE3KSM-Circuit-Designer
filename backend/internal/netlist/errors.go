package netlist

import "fmt"

// ErrUnsupported is returned by Parse when the source contains a construct
// that milestone 1 deliberately does not implement (e.g. a .MODEL block,
// behavioral B-source, or transistor element). Returning a structured error
// lets later milestones detect and extend the gaps without grepping strings.
type ErrUnsupported struct {
	Line       int    // 1-based source line number
	Directive  string // for unsupported .X directives, e.g. ".MODEL"
	Refdesig   string // for unsupported component prefixes, e.g. "Q"
	SourceMode string // for unsupported source statement keywords, e.g. "PULSE"
	Detail     string // free-text context
}

// Error implements the error interface.
func (e ErrUnsupported) Error() string {
	what := e.Detail
	switch {
	case e.Directive != "":
		what = fmt.Sprintf("directive %s", e.Directive)
	case e.Refdesig != "":
		what = fmt.Sprintf("component prefix %q", e.Refdesig)
	case e.SourceMode != "":
		what = fmt.Sprintf("source mode %s", e.SourceMode)
	}
	if e.Line > 0 {
		return fmt.Sprintf("line %d: unsupported %s (milestone 1 scope)", e.Line, what)
	}
	return fmt.Sprintf("unsupported %s (milestone 1 scope)", what)
}

// ParseError wraps any parse failure with line context. Construct via
// errorAt below.
type ParseError struct {
	Line int
	Msg  string
	Err  error
}

func (p ParseError) Error() string {
	if p.Err != nil {
		return fmt.Sprintf("line %d: %s: %v", p.Line, p.Msg, p.Err)
	}
	return fmt.Sprintf("line %d: %s", p.Line, p.Msg)
}

func (p ParseError) Unwrap() error { return p.Err }

func errorAt(line int, msg string) error {
	return ParseError{Line: line, Msg: msg}
}

func errorAtf(line int, format string, args ...interface{}) error {
	return ParseError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

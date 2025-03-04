// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regtest

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/tools/internal/lsp"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/span"
)

// An Expectation asserts that the state of the editor at a point in time
// matches an expected condition. This is used for signaling in tests when
// certain conditions in the editor are met.
type Expectation interface {
	// Check determines whether the state of the editor satisfies the
	// expectation, returning the results that met the condition.
	Check(State) Verdict
	// Description is a human-readable description of the expectation.
	Description() string
}

var (
	// InitialWorkspaceLoad is an expectation that the workspace initial load has
	// completed. It is verified via workdone reporting.
	InitialWorkspaceLoad = CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromInitialWorkspaceLoad), 1)
)

// A Verdict is the result of checking an expectation against the current
// editor state.
type Verdict int

// Order matters for the following constants: verdicts are sorted in order of
// decisiveness.
const (
	// Met indicates that an expectation is satisfied by the current state.
	Met Verdict = iota
	// Unmet indicates that an expectation is not currently met, but could be met
	// in the future.
	Unmet
	// Unmeetable indicates that an expectation cannot be satisfied in the
	// future.
	Unmeetable
)

func (v Verdict) String() string {
	switch v {
	case Met:
		return "Met"
	case Unmet:
		return "Unmet"
	case Unmeetable:
		return "Unmeetable"
	}
	return fmt.Sprintf("unrecognized verdict %d", v)
}

// SimpleExpectation holds an arbitrary check func, and implements the Expectation interface.
type SimpleExpectation struct {
	check       func(State) Verdict
	description string
}

// Check invokes e.check.
func (e SimpleExpectation) Check(s State) Verdict {
	return e.check(s)
}

// Description returns e.descriptin.
func (e SimpleExpectation) Description() string {
	return e.description
}

// OnceMet returns an Expectation that, once the precondition is met, asserts
// that mustMeet is met.
func OnceMet(precondition Expectation, mustMeet Expectation) *SimpleExpectation {
	check := func(s State) Verdict {
		switch pre := precondition.Check(s); pre {
		case Unmeetable:
			return Unmeetable
		case Met:
			verdict := mustMeet.Check(s)
			if verdict != Met {
				return Unmeetable
			}
			return Met
		default:
			return Unmet
		}
	}
	return &SimpleExpectation{
		check:       check,
		description: fmt.Sprintf("once %q is met, must have %q", precondition.Description(), mustMeet.Description()),
	}
}

// ReadDiagnostics is an 'expectation' that is used to read diagnostics
// atomically. It is intended to be used with 'OnceMet'.
func ReadDiagnostics(fileName string, into *protocol.PublishDiagnosticsParams) *SimpleExpectation {
	check := func(s State) Verdict {
		diags, ok := s.diagnostics[fileName]
		if !ok {
			return Unmeetable
		}
		*into = *diags
		return Met
	}
	return &SimpleExpectation{
		check:       check,
		description: fmt.Sprintf("read diagnostics for %q", fileName),
	}
}

// NoOutstandingWork asserts that there is no work initiated using the LSP
// $/progress API that has not completed.
func NoOutstandingWork() SimpleExpectation {
	check := func(s State) Verdict {
		if len(s.outstandingWork) == 0 {
			return Met
		}
		return Unmet
	}
	return SimpleExpectation{
		check:       check,
		description: "no outstanding work",
	}
}

// NoShowMessage asserts that the editor has not received a ShowMessage.
func NoShowMessage() SimpleExpectation {
	check := func(s State) Verdict {
		if len(s.showMessage) == 0 {
			return Met
		}
		return Unmeetable
	}
	return SimpleExpectation{
		check:       check,
		description: "no ShowMessage received",
	}
}

// ShownMessage asserts that the editor has received a ShownMessage with the
// given title.
func ShownMessage(title string) SimpleExpectation {
	check := func(s State) Verdict {
		for _, m := range s.showMessage {
			if strings.Contains(m.Message, title) {
				return Met
			}
		}
		return Unmet
	}
	return SimpleExpectation{
		check:       check,
		description: "received ShowMessage",
	}
}

// ShowMessageRequest asserts that the editor has received a ShowMessageRequest
// with an action item that has the given title.
func ShowMessageRequest(title string) SimpleExpectation {
	check := func(s State) Verdict {
		if len(s.showMessageRequest) == 0 {
			return Unmet
		}
		// Only check the most recent one.
		m := s.showMessageRequest[len(s.showMessageRequest)-1]
		if len(m.Actions) == 0 || len(m.Actions) > 1 {
			return Unmet
		}
		if m.Actions[0].Title == title {
			return Met
		}
		return Unmet
	}
	return SimpleExpectation{
		check:       check,
		description: "received ShowMessageRequest",
	}
}

// CompletedWork expects a work item to have been completed >= atLeast times.
//
// Since the Progress API doesn't include any hidden metadata, we must use the
// progress notification title to identify the work we expect to be completed.
func CompletedWork(title string, atLeast int) SimpleExpectation {
	check := func(s State) Verdict {
		if s.completedWork[title] >= atLeast {
			return Met
		}
		return Unmet
	}
	return SimpleExpectation{
		check:       check,
		description: fmt.Sprintf("completed work %q at least %d time(s)", title, atLeast),
	}
}

// LogExpectation is an expectation on the log messages received by the editor
// from gopls.
type LogExpectation struct {
	check       func([]*protocol.LogMessageParams) Verdict
	description string
}

// Check implements the Expectation interface.
func (e LogExpectation) Check(s State) Verdict {
	return e.check(s.logs)
}

// Description implements the Expectation interface.
func (e LogExpectation) Description() string {
	return e.description
}

// NoErrorLogs asserts that the client has not received any log messages of
// error severity.
func NoErrorLogs() LogExpectation {
	return NoLogMatching(protocol.Error, "")
}

// LogMatching asserts that the client has received a log message
// of type typ matching the regexp re.
func LogMatching(typ protocol.MessageType, re string, count int) LogExpectation {
	rec, err := regexp.Compile(re)
	if err != nil {
		panic(err)
	}
	check := func(msgs []*protocol.LogMessageParams) Verdict {
		var found int
		for _, msg := range msgs {
			if msg.Type == typ && rec.Match([]byte(msg.Message)) {
				found++
			}
		}
		if found == count {
			return Met
		}
		return Unmet
	}
	return LogExpectation{
		check:       check,
		description: fmt.Sprintf("log message matching %q", re),
	}
}

// NoLogMatching asserts that the client has not received a log message
// of type typ matching the regexp re. If re is an empty string, any log
// message is considered a match.
func NoLogMatching(typ protocol.MessageType, re string) LogExpectation {
	var r *regexp.Regexp
	if re != "" {
		var err error
		r, err = regexp.Compile(re)
		if err != nil {
			panic(err)
		}
	}
	check := func(msgs []*protocol.LogMessageParams) Verdict {
		for _, msg := range msgs {
			if msg.Type != typ {
				continue
			}
			if r == nil || r.Match([]byte(msg.Message)) {
				return Unmeetable
			}
		}
		return Met
	}
	return LogExpectation{
		check:       check,
		description: fmt.Sprintf("no log message matching %q", re),
	}
}

// RegistrationExpectation is an expectation on the capability registrations
// received by the editor from gopls.
type RegistrationExpectation struct {
	check       func([]*protocol.RegistrationParams) Verdict
	description string
}

// Check implements the Expectation interface.
func (e RegistrationExpectation) Check(s State) Verdict {
	return e.check(s.registrations)
}

// Description implements the Expectation interface.
func (e RegistrationExpectation) Description() string {
	return e.description
}

// RegistrationMatching asserts that the client has received a capability
// registration matching the given regexp.
func RegistrationMatching(re string) RegistrationExpectation {
	rec, err := regexp.Compile(re)
	if err != nil {
		panic(err)
	}
	check := func(params []*protocol.RegistrationParams) Verdict {
		for _, p := range params {
			for _, r := range p.Registrations {
				if rec.Match([]byte(r.Method)) {
					return Met
				}
			}
		}
		return Unmet
	}
	return RegistrationExpectation{
		check:       check,
		description: fmt.Sprintf("registration matching %q", re),
	}
}

// UnregistrationExpectation is an expectation on the capability
// unregistrations received by the editor from gopls.
type UnregistrationExpectation struct {
	check       func([]*protocol.UnregistrationParams) Verdict
	description string
}

// Check implements the Expectation interface.
func (e UnregistrationExpectation) Check(s State) Verdict {
	return e.check(s.unregistrations)
}

// Description implements the Expectation interface.
func (e UnregistrationExpectation) Description() string {
	return e.description
}

// UnregistrationMatching asserts that the client has received an
// unregistration whose ID matches the given regexp.
func UnregistrationMatching(re string) UnregistrationExpectation {
	rec, err := regexp.Compile(re)
	if err != nil {
		panic(err)
	}
	check := func(params []*protocol.UnregistrationParams) Verdict {
		for _, p := range params {
			for _, r := range p.Unregisterations {
				if rec.Match([]byte(r.Method)) {
					return Met
				}
			}
		}
		return Unmet
	}
	return UnregistrationExpectation{
		check:       check,
		description: fmt.Sprintf("unregistration matching %q", re),
	}
}

// A DiagnosticExpectation is a condition that must be met by the current set
// of diagnostics for a file.
type DiagnosticExpectation struct {
	// IsMet determines whether the diagnostics for this file version satisfy our
	// expectation.
	isMet func(*protocol.PublishDiagnosticsParams) bool
	// Description is a human-readable description of the diagnostic expectation.
	description string
	// Path is the scratch workdir-relative path to the file being asserted on.
	path string
}

// Check implements the Expectation interface.
func (e DiagnosticExpectation) Check(s State) Verdict {
	if diags, ok := s.diagnostics[e.path]; ok && e.isMet(diags) {
		return Met
	}
	return Unmet
}

// Description implements the Expectation interface.
func (e DiagnosticExpectation) Description() string {
	return fmt.Sprintf("%s: %s", e.path, e.description)
}

// EmptyDiagnostics asserts that empty diagnostics are sent for the
// workspace-relative path name.
func EmptyDiagnostics(name string) Expectation {
	check := func(s State) Verdict {
		if diags := s.diagnostics[name]; diags != nil && len(diags.Diagnostics) == 0 {
			return Met
		}
		return Unmet
	}
	return SimpleExpectation{
		check:       check,
		description: "empty diagnostics",
	}
}

// NoDiagnostics asserts that no diagnostics are sent for the
// workspace-relative path name. It should be used primarily in conjunction
// with a OnceMet, as it has to check that all outstanding diagnostics have
// already been delivered.
func NoDiagnostics(name string) Expectation {
	check := func(s State) Verdict {
		if _, ok := s.diagnostics[name]; !ok {
			return Met
		}
		return Unmet
	}
	return SimpleExpectation{
		check:       check,
		description: "no diagnostics",
	}
}

// AnyDiagnosticAtCurrentVersion asserts that there is a diagnostic report for
// the current edited version of the buffer corresponding to the given
// workdir-relative pathname.
func (e *Env) AnyDiagnosticAtCurrentVersion(name string) DiagnosticExpectation {
	version := e.Editor.BufferVersion(name)
	isMet := func(diags *protocol.PublishDiagnosticsParams) bool {
		return int(diags.Version) == version
	}
	return DiagnosticExpectation{
		isMet:       isMet,
		description: fmt.Sprintf("any diagnostics at version %d", version),
		path:        name,
	}
}

// DiagnosticAtRegexp expects that there is a diagnostic entry at the start
// position matching the regexp search string re in the buffer specified by
// name. Note that this currently ignores the end position.
func (e *Env) DiagnosticAtRegexp(name, re string) DiagnosticExpectation {
	e.T.Helper()
	pos := e.RegexpSearch(name, re)
	expectation := DiagnosticAt(name, pos.Line, pos.Column)
	expectation.description += fmt.Sprintf(" (location of %q)", re)
	return expectation
}

// DiagnosticAt asserts that there is a diagnostic entry at the position
// specified by line and col, for the workdir-relative path name.
func DiagnosticAt(name string, line, col int) DiagnosticExpectation {
	isMet := func(diags *protocol.PublishDiagnosticsParams) bool {
		for _, d := range diags.Diagnostics {
			if d.Range.Start.Line == float64(line) && d.Range.Start.Character == float64(col) {
				return true
			}
		}
		return false
	}
	return DiagnosticExpectation{
		isMet:       isMet,
		description: fmt.Sprintf("diagnostic at {line:%d, column:%d}", line, col),
		path:        name,
	}
}

// NoDiagnosticAtRegexp expects that there is no diagnostic entry at the start
// position matching the regexp search string re in the buffer specified by
// name. Note that this currently ignores the end position.
// This should only be used in combination with OnceMet for a given condition,
// otherwise it may always succeed.
func (e *Env) NoDiagnosticAtRegexp(name, re string) DiagnosticExpectation {
	e.T.Helper()
	pos := e.RegexpSearch(name, re)
	expectation := NoDiagnosticAt(name, pos.Line, pos.Column)
	expectation.description += fmt.Sprintf(" (location of %q)", re)
	return expectation
}

// NoDiagnosticAt asserts that there is no diagnostic entry at the position
// specified by line and col, for the workdir-relative path name.
// This should only be used in combination with OnceMet for a given condition,
// otherwise it may always succeed.
func NoDiagnosticAt(name string, line, col int) DiagnosticExpectation {
	isMet := func(diags *protocol.PublishDiagnosticsParams) bool {
		for _, d := range diags.Diagnostics {
			if d.Range.Start.Line == float64(line) && d.Range.Start.Character == float64(col) {
				return false
			}
		}
		return true
	}
	return DiagnosticExpectation{
		isMet:       isMet,
		description: fmt.Sprintf("no diagnostic at {line:%d, column:%d}", line, col),
		path:        name,
	}
}

// NoDiagnosticWithMessage asserts that there is no diagnostic entry with the
// given message.
//
// This should only be used in combination with OnceMet for a given condition,
// otherwise it may always succeed.
func NoDiagnosticWithMessage(msg string) DiagnosticExpectation {
	var uri span.URI
	isMet := func(diags *protocol.PublishDiagnosticsParams) bool {
		for _, d := range diags.Diagnostics {
			if d.Message == msg {
				return true
			}
		}
		return false
	}
	var path string
	if uri != "" {
		path = uri.Filename()
	}
	return DiagnosticExpectation{
		isMet:       isMet,
		description: fmt.Sprintf("no diagnostic with message %s", msg),
		path:        path,
	}
}

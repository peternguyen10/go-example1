// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regtest

import (
	"strings"
	"testing"

	"golang.org/x/tools/internal/lsp"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/tests"
	"golang.org/x/tools/internal/testenv"
)

const proxy = `
-- example.com@v1.2.3/go.mod --
module example.com

go 1.12
-- example.com@v1.2.3/blah/blah.go --
package blah

const Name = "Blah"
-- random.org@v1.2.3/go.mod --
module random.org

go 1.12
-- random.org@v1.2.3/blah/blah.go --
package hello

const Name = "Hello"
`

func TestModFileModification(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const untidyModule = `
-- go.mod --
module mod.com

-- main.go --
package main

import "example.com/blah"

func main() {
	println(blah.Name)
}
`
	t.Run("basic", func(t *testing.T) {
		withOptions(WithProxyFiles(proxy)).run(t, untidyModule, func(t *testing.T, env *Env) {
			// Open the file and make sure that the initial workspace load does not
			// modify the go.mod file.
			goModContent := env.ReadWorkspaceFile("go.mod")
			env.OpenFile("main.go")
			env.Await(
				env.DiagnosticAtRegexp("main.go", "\"example.com/blah\""),
			)
			if got := env.ReadWorkspaceFile("go.mod"); got != goModContent {
				t.Fatalf("go.mod changed on disk:\n%s", tests.Diff(goModContent, got))
			}
			// Save the buffer, which will format and organize imports.
			// Confirm that the go.mod file still does not change.
			env.SaveBuffer("main.go")
			env.Await(
				env.DiagnosticAtRegexp("main.go", "\"example.com/blah\""),
			)
			if got := env.ReadWorkspaceFile("go.mod"); got != goModContent {
				t.Fatalf("go.mod changed on disk:\n%s", tests.Diff(goModContent, got))
			}
		})
	})

	// Reproduce golang/go#40269 by deleting and recreating main.go.
	t.Run("delete main.go", func(t *testing.T) {
		t.Skip("This test will be flaky until golang/go#40269 is resolved.")

		withOptions(WithProxyFiles(proxy)).run(t, untidyModule, func(t *testing.T, env *Env) {
			goModContent := env.ReadWorkspaceFile("go.mod")
			mainContent := env.ReadWorkspaceFile("main.go")
			env.OpenFile("main.go")
			env.SaveBuffer("main.go")

			env.RemoveWorkspaceFile("main.go")
			env.Await(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1),
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidSave), 1),
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChangeWatchedFiles), 2),
			)

			env.WriteWorkspaceFile("main.go", mainContent)
			env.Await(
				env.DiagnosticAtRegexp("main.go", "\"example.com/blah\""),
			)
			if got := env.ReadWorkspaceFile("go.mod"); got != goModContent {
				t.Fatalf("go.mod changed on disk:\n%s", tests.Diff(goModContent, got))
			}
		})
	})
}

// Tests that multiple missing dependencies gives good single fixes.
func TestMissingDependencyFixes(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)
	const mod = `
-- go.mod --
module mod.com

go 1.12

-- main.go --
package main

import "example.com/blah"
import "random.org/blah"

var _, _ = blah.Name, hello.Name
`

	const want = `module mod.com

go 1.12

require random.org v1.2.3
`

	withOptions(WithProxyFiles(proxy)).run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				env.DiagnosticAtRegexp("main.go", `"random.org/blah"`),
				ReadDiagnostics("main.go", &d),
			),
		)
		var randomDiag protocol.Diagnostic
		for _, diag := range d.Diagnostics {
			if strings.Contains(diag.Message, "random.org") {
				randomDiag = diag
			}
		}
		env.OpenFile("go.mod")
		env.ApplyQuickFixes("main.go", []protocol.Diagnostic{randomDiag})
		if got := env.Editor.BufferText("go.mod"); got != want {
			t.Fatalf("unexpected go.mod content:\n%s", tests.Diff(want, got))
		}
	})
}

func TestIndirectDependencyFix(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const mod = `
-- go.mod --
module mod.com

go 1.12

require example.com v1.2.3 // indirect
-- main.go --
package main

import "example.com/blah"

func main() {
	fmt.Println(blah.Name)
`
	const want = `module mod.com

go 1.12

require example.com v1.2.3
`
	runner.Run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				env.DiagnosticAtRegexp("go.mod", "// indirect"),
				ReadDiagnostics("go.mod", &d),
			),
		)
		env.ApplyQuickFixes("go.mod", d.Diagnostics)
		if got := env.Editor.BufferText("go.mod"); got != want {
			t.Fatalf("unexpected go.mod content:\n%s", tests.Diff(want, got))
		}
	}, WithProxyFiles(proxy))
}

func TestUnusedDiag(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const proxy = `
-- example.com@v1.0.0/x.go --
package pkg
const X = 1
`
	const files = `
-- go.mod --
module mod.com
go 1.14
require example.com v1.0.0

-- main.go --
package main
func main() {}
`

	const want = `module mod.com

go 1.14
`

	withOptions(WithProxyFiles(proxy)).run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				env.DiagnosticAtRegexp("go.mod", `require example.com`),
				ReadDiagnostics("go.mod", &d),
			),
		)
		env.ApplyQuickFixes("go.mod", d.Diagnostics)
		if got := env.Editor.BufferText("go.mod"); got != want {
			t.Fatalf("unexpected go.mod content:\n%s", tests.Diff(want, got))
		}
	})
}

// Test to reproduce golang/go#39041. It adds a new require to a go.mod file
// that already has an unused require.
func TestNewDepWithUnusedDep(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const proxy = `
-- github.com/esimov/caire@v1.2.5/go.mod --
module github.com/esimov/caire

go 1.12
-- github.com/esimov/caire@v1.2.5/caire.go --
package caire

func RemoveTempImage() {}
-- google.golang.org/protobuf@v1.20.0/go.mod --
module google.golang.org/protobuf

go 1.12
-- google.golang.org/protobuf@v1.20.0/hello/hello.go --
package hello
`
	const repro = `
-- go.mod --
module mod.com

go 1.14

require google.golang.org/protobuf v1.20.0
-- main.go --
package main

import (
    "github.com/esimov/caire"
)

func _() {
    caire.RemoveTempImage()
}`
	runner.Run(t, repro, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.OpenFile("main.go")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				env.DiagnosticAtRegexp("main.go", `"github.com/esimov/caire"`),
				ReadDiagnostics("main.go", &d),
			),
		)
		env.ApplyQuickFixes("main.go", d.Diagnostics)
		want := `module mod.com

go 1.14

require (
	github.com/esimov/caire v1.2.5
	google.golang.org/protobuf v1.20.0
)
`
		if got := env.Editor.BufferText("go.mod"); got != want {
			t.Fatalf("TestNewDepWithUnusedDep failed:\n%s", tests.Diff(want, got))
		}
	}, WithProxyFiles(proxy))
}

// TODO: For this test to be effective, the sandbox's file watcher must respect
// the file watching GlobPattern in the capability registration. See
// golang/go#39384.
func TestModuleChangesOnDisk(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const mod = `
-- go.mod --
module mod.com

go 1.12

require example.com v1.2.3
-- main.go --
package main

func main() {
	fmt.Println(blah.Name)
`
	runner.Run(t, mod, func(t *testing.T, env *Env) {
		env.Await(env.DiagnosticAtRegexp("go.mod", "require"))
		env.RunGoCommand("mod", "tidy")
		env.Await(
			EmptyDiagnostics("go.mod"),
		)
	}, WithProxyFiles(proxy))
}

func TestBadlyVersionedModule(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const badModule = `
-- example.com/blah/@v/list --
v1.0.0
-- example.com/blah/@v/v1.0.0.mod --
module example.com

go 1.12
-- example.com/blah@v1.0.0/blah.go --
package blah

const Name = "Blah"
-- example.com/blah@v1.0.0/blah_test.go --
package blah_test

import (
	"testing"
)

func TestBlah(t *testing.T) {}

-- example.com/blah/v2/@v/list --
v2.0.0
-- example.com/blah/v2/@v/v2.0.0.mod --
module example.com

go 1.12
-- example.com/blah/v2@v2.0.0/blah.go --
package blah

const Name = "Blah"
-- example.com/blah/v2@v2.0.0/blah_test.go --
package blah_test

import (
	"testing"

	"example.com/blah"
)

func TestBlah(t *testing.T) {}
`
	const pkg = `
-- go.mod --
module mod.com

require (
	example.com/blah/v2 v2.0.0
)
-- main.go --
package main

import "example.com/blah/v2"

func main() {
	println(blah.Name)
}
`
	runner.Run(t, pkg, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.OpenFile("go.mod")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				DiagnosticAt("go.mod", 0, 0),
				ReadDiagnostics("go.mod", &d),
			),
		)
		env.ApplyQuickFixes("main.go", d.Diagnostics)
		const want = `module mod.com

require (
	example.com/blah v1.0.0
	example.com/blah/v2 v2.0.0
)
`
		if got := env.Editor.BufferText("go.mod"); got != want {
			t.Fatalf("suggested fixes failed:\n%s", tests.Diff(want, got))
		}
	}, WithProxyFiles(badModule))
}

// Reproduces golang/go#38232.
func TestUnknownRevision(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const unknown = `
-- go.mod --
module mod.com

require (
	example.com v1.2.2
)
-- main.go --
package main

import "example.com/blah"

func main() {
	var x = blah.Name
}
`

	// Start from a bad state/bad IWL, and confirm that we recover.
	t.Run("bad", func(t *testing.T) {
		runner.Run(t, unknown, func(t *testing.T, env *Env) {
			env.OpenFile("go.mod")
			env.Await(
				env.DiagnosticAtRegexp("go.mod", "example.com v1.2.2"),
			)
			env.RegexpReplace("go.mod", "v1.2.2", "v1.2.3")
			env.Editor.SaveBufferWithoutActions(env.Ctx, "go.mod") // go.mod changes must be on disk
			env.Await(
				env.DiagnosticAtRegexp("main.go", "x = "),
			)
		}, WithProxyFiles(proxy))
	})

	const known = `
-- go.mod --
module mod.com

require (
	example.com v1.2.3
)
-- main.go --
package main

import "example.com/blah"

func main() {
	var x = blah.Name
}
`
	// Start from a good state, transform to a bad state, and confirm that we
	// still recover.
	t.Run("good", func(t *testing.T) {
		runner.Run(t, known, func(t *testing.T, env *Env) {
			env.OpenFile("go.mod")
			env.Await(
				env.DiagnosticAtRegexp("main.go", "x = "),
			)
			env.RegexpReplace("go.mod", "v1.2.3", "v1.2.2")
			env.Editor.SaveBufferWithoutActions(env.Ctx, "go.mod") // go.mod changes must be on disk
			env.Await(
				env.DiagnosticAtRegexp("go.mod", "example.com v1.2.2"),
			)
			env.RegexpReplace("go.mod", "v1.2.2", "v1.2.3")
			env.Editor.SaveBufferWithoutActions(env.Ctx, "go.mod") // go.mod changes must be on disk
			env.Await(
				env.DiagnosticAtRegexp("main.go", "x = "),
			)
		}, WithProxyFiles(proxy))
	})
}

func TestTidyOnSave(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const untidyModule = `
-- go.mod --
module mod.com

go 1.14

require random.org v1.2.3
-- main.go --
package main

import "example.com/blah"

func main() {
	fmt.Println(blah.Name)
}
`
	withOptions(WithProxyFiles(proxy)).run(t, untidyModule, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.Await(
			env.DiagnosticAtRegexp("main.go", `"example.com/blah"`),
			env.DiagnosticAtRegexp("go.mod", `require random.org v1.2.3`),
		)
		env.SaveBuffer("go.mod")
		const want = `module mod.com

go 1.14

require example.com v1.2.3
`
		if got := env.ReadWorkspaceFile("go.mod"); got != want {
			t.Fatalf("unexpected go.mod content:\n%s", tests.Diff(want, got))
		}
	})
}

// Confirm that an error in an indirect dependency of a requirement is surfaced
// as a diagnostic in the go.mod file.
func TestErrorInIndirectDependency(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const badProxy = `
-- example.com@v1.2.3/go.mod --
module example.com

go 1.12

require random.org v1.2.3 // indirect
-- example.com@v1.2.3/blah/blah.go --
package blah

const Name = "Blah"
-- random.org@v1.2.3/go.mod --
module bob.org

go 1.12
-- random.org@v1.2.3/blah/blah.go --
package hello

const Name = "Hello"
`
	const module = `
-- go.mod --
module mod.com

go 1.14

require example.com v1.2.3
-- main.go --
package main

import "example.com/blah"

func main() {
	println(blah.Name)
}
`
	withOptions(WithProxyFiles(badProxy)).run(t, module, func(t *testing.T, env *Env) {
		env.OpenFile("go.mod")
		env.Await(
			env.DiagnosticAtRegexp("go.mod", "require example.com v1.2.3"),
		)
	})
}

// A copy of govim's config_set_env_goflags_mod_readonly test.
func TestGovimModReadonly(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com

go 1.13
-- main.go --
package main

import "example.com/blah"

func main() {
	println(blah.Name)
}
`
	withOptions(
		EditorConfig{
			Env: map[string]string{
				"GOFLAGS": "-mod=readonly",
			},
		},
		WithProxyFiles(proxy),
		WithModes(WithoutExperiments),
	).run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		original := env.ReadWorkspaceFile("go.mod")
		env.Await(
			env.DiagnosticAtRegexp("main.go", `"example.com/blah"`),
		)
		got := env.ReadWorkspaceFile("go.mod")
		if got != original {
			t.Fatalf("go.mod file modified:\n%s", tests.Diff(original, got))
		}
		env.RunGoCommand("get", "example.com/blah@v1.2.3")
		env.RunGoCommand("mod", "tidy")
		env.Await(
			EmptyDiagnostics("main.go"),
		)
	})
}

func TestMultiModuleModDiagnostics(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const mod = `
-- a/go.mod --
module mod.com

go 1.14

require (
	example.com v1.2.3
)
-- a/main.go --
package main

func main() {}
-- b/go.mod --
module mod.com

go 1.14
-- b/main.go --
package main

import "example.com/blah"

func main() {
	blah.SaySomething()
}
`
	withOptions(
		WithProxyFiles(workspaceProxy),
		WithModes(Experimental),
	).run(t, mod, func(t *testing.T, env *Env) {
		env.Await(
			env.DiagnosticAtRegexp("a/go.mod", "example.com v1.2.3"),
			env.DiagnosticAtRegexp("b/go.mod", "module mod.com"),
		)
	})
}

func TestModTidyWithBuildTags(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

	const mod = `
-- go.mod --
module mod.com

go 1.14
-- main.go --
// +build bob

package main

import "example.com/blah"

func main() {
	blah.SaySomething()
}
`
	withOptions(
		WithProxyFiles(workspaceProxy),
		EditorConfig{
			BuildFlags: []string{"-tags", "bob"},
		},
	).run(t, mod, func(t *testing.T, env *Env) {
		env.Await(
			env.DiagnosticAtRegexp("main.go", `"example.com/blah"`),
		)
	})
}

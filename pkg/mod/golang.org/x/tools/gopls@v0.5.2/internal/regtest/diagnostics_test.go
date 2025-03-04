// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regtest

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"golang.org/x/tools/internal/lsp"
	"golang.org/x/tools/internal/lsp/fake"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/tests"
	"golang.org/x/tools/internal/testenv"
)

// Use mod.com for all go.mod files due to golang/go#35230.
const exampleProgram = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

import "fmt"

func main() {
	fmt.Println("Hello World.")
}`

func TestDiagnosticErrorInEditedFile(t *testing.T) {
	// This test is very basic: start with a clean Go program, make an error, and
	// get a diagnostic for that error. However, it also demonstrates how to
	// combine Expectations to await more complex state in the editor.
	runner.Run(t, exampleProgram, func(t *testing.T, env *Env) {
		// Deleting the 'n' at the end of Println should generate a single error
		// diagnostic.
		env.OpenFile("main.go")
		env.RegexpReplace("main.go", "Printl(n)", "")
		env.Await(
			// Once we have gotten diagnostics for the change above, we should
			// satisfy the DiagnosticAtRegexp assertion.
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1),
				env.DiagnosticAtRegexp("main.go", "Printl"),
			),
			// Assert that this test has sent no error logs to the client. This is not
			// strictly necessary for testing this regression, but is included here
			// as an example of using the NoErrorLogs() expectation. Feel free to
			// delete.
			NoErrorLogs(),
		)
	})
}

func TestMissingImportDiagsClearOnFirstFile(t *testing.T) {
	const onlyMod = `
-- go.mod --
module mod.com

go 1.12
`
	runner.Run(t, onlyMod, func(t *testing.T, env *Env) {
		env.CreateBuffer("main.go", `package main

func m() {
	log.Println()
}
`)
		env.Await(
			env.DiagnosticAtRegexp("main.go", "log"),
		)
		env.SaveBuffer("main.go")
		env.Await(
			EmptyDiagnostics("main.go"),
		)
	})
}

func TestDiagnosticErrorInNewFile(t *testing.T) {
	const brokenFile = `package main

const Foo = "abc
`
	runner.Run(t, brokenFile, func(t *testing.T, env *Env) {
		env.CreateBuffer("broken.go", brokenFile)
		env.Await(env.DiagnosticAtRegexp("broken.go", "\"abc"))
	})
}

// badPackage contains a duplicate definition of the 'a' const.
const badPackage = `
-- go.mod --
module mod.com

go 1.12
-- a.go --
package consts

const a = 1
-- b.go --
package consts

const a = 2
`

func TestDiagnosticClearingOnEdit(t *testing.T) {
	runner.Run(t, badPackage, func(t *testing.T, env *Env) {
		env.OpenFile("b.go")
		env.Await(env.DiagnosticAtRegexp("a.go", "a = 1"), env.DiagnosticAtRegexp("b.go", "a = 2"))

		// Fix the error by editing the const name in b.go to `b`.
		env.RegexpReplace("b.go", "(a) = 2", "b")
		env.Await(
			EmptyDiagnostics("a.go"),
			EmptyDiagnostics("b.go"),
		)
	})
}

func TestDiagnosticClearingOnDelete_Issue37049(t *testing.T) {
	runner.Run(t, badPackage, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.Await(env.DiagnosticAtRegexp("a.go", "a = 1"), env.DiagnosticAtRegexp("b.go", "a = 2"))
		env.RemoveWorkspaceFile("b.go")

		env.Await(EmptyDiagnostics("a.go"), EmptyDiagnostics("b.go"))
	})
}

func TestDiagnosticClearingOnClose(t *testing.T) {
	runner.Run(t, badPackage, func(t *testing.T, env *Env) {
		env.CreateBuffer("c.go", `package consts

const a = 3`)
		env.Await(
			env.DiagnosticAtRegexp("a.go", "a = 1"),
			env.DiagnosticAtRegexp("b.go", "a = 2"),
			env.DiagnosticAtRegexp("c.go", "a = 3"))
		env.CloseBuffer("c.go")
		env.Await(
			env.DiagnosticAtRegexp("a.go", "a = 1"),
			env.DiagnosticAtRegexp("b.go", "a = 2"),
			EmptyDiagnostics("c.go"))
	})
}

// Tests golang/go#37978.
func TestIssue37978(t *testing.T) {
	runner.Run(t, exampleProgram, func(t *testing.T, env *Env) {
		// Create a new workspace-level directory and empty file.
		env.CreateBuffer("c/c.go", "")

		// Write the file contents with a missing import.
		env.EditBuffer("c/c.go", fake.Edit{
			Text: `package c

const a = http.MethodGet
`,
		})
		env.Await(
			env.DiagnosticAtRegexp("c/c.go", "http.MethodGet"),
		)
		// Save file, which will organize imports, adding the expected import.
		// Expect the diagnostics to clear.
		env.SaveBuffer("c/c.go")
		env.Await(
			EmptyDiagnostics("c/c.go"),
		)
	})
}

// Tests golang/go#38878: good a.go, bad a_test.go, remove a_test.go but its errors remain
// If the file is open in the editor, this is working as intended
// If the file is not open in the editor, the errors go away
const test38878 = `
-- go.mod --
module foo

-- a.go --
package x

func f() {}

-- a_test.go --
package x

import "testing"

func TestA(t *testing.T) {
	f(3)
}
`

func TestRmTest38878Close(t *testing.T) {
	runner.Run(t, test38878, func(t *testing.T, env *Env) {
		env.OpenFile("a_test.go")
		env.Await(DiagnosticAt("a_test.go", 5, 3))
		env.CloseBuffer("a_test.go")
		env.RemoveWorkspaceFile("a_test.go")
		// diagnostics go away
		env.Await(EmptyDiagnostics("a_test.go"))
	})
}

func TestRmTest38878(t *testing.T) {
	log.SetFlags(log.Lshortfile)
	runner.Run(t, test38878, func(t *testing.T, env *Env) {
		env.OpenFile("a_test.go")
		env.Await(DiagnosticAt("a_test.go", 5, 3))
		env.Sandbox.Workdir.RemoveFile(context.Background(), "a_test.go")
		// diagnostics remain after giving gopls a chance to do something
		// (there is not yet a better way to decide gopls isn't going
		// to do anything)
		time.Sleep(time.Second)
		env.Await(DiagnosticAt("a_test.go", 5, 3))
	})
}

// TestNoMod confirms that gopls continues to work when a user adds a go.mod
// file to their workspace.
func TestNoMod(t *testing.T) {
	const noMod = `
-- main.go --
package main

import "mod.com/bob"

func main() {
	bob.Hello()
}
-- bob/bob.go --
package bob

func Hello() {
	var x int
}
`

	t.Run("manual", func(t *testing.T) {
		runner.Run(t, noMod, func(t *testing.T, env *Env) {
			env.Await(
				env.DiagnosticAtRegexp("main.go", `"mod.com/bob"`),
			)
			env.CreateBuffer("go.mod", `module mod.com

	go 1.12
`)
			env.SaveBuffer("go.mod")
			env.Await(
				EmptyDiagnostics("main.go"),
			)
			var d protocol.PublishDiagnosticsParams
			env.Await(
				OnceMet(
					env.DiagnosticAtRegexp("bob/bob.go", "x"),
					ReadDiagnostics("bob/bob.go", &d),
				),
			)
			if len(d.Diagnostics) != 1 {
				t.Fatalf("expected 1 diagnostic, got %v", len(d.Diagnostics))
			}
		})
	})
	t.Run("initialized", func(t *testing.T) {
		runner.Run(t, noMod, func(t *testing.T, env *Env) {
			env.Await(
				env.DiagnosticAtRegexp("main.go", `"mod.com/bob"`),
			)
			env.RunGoCommand("mod", "init", "mod.com")
			env.Await(
				EmptyDiagnostics("main.go"),
				env.DiagnosticAtRegexp("bob/bob.go", "x"),
			)
		})
	})

	t.Run("without workspace module", func(t *testing.T) {
		withOptions(
			WithModes(WithoutExperiments),
		).run(t, noMod, func(t *testing.T, env *Env) {
			env.Await(
				env.DiagnosticAtRegexp("main.go", `"mod.com/bob"`),
			)
			if err := env.Sandbox.RunGoCommand(env.Ctx, "", "mod", []string{"init", "mod.com"}); err != nil {
				t.Fatal(err)
			}
			env.Await(
				EmptyDiagnostics("main.go"),
				env.DiagnosticAtRegexp("bob/bob.go", "x"),
			)
		})
	})
}

// Tests golang/go#38267.
func TestIssue38267(t *testing.T) {
	const testPackage = `
-- go.mod --
module mod.com

go 1.12
-- lib.go --
package lib

func Hello(x string) {
	_ = x
}
-- lib_test.go --
package lib

import "testing"

type testStruct struct{
	name string
}

func TestHello(t *testing.T) {
	testStructs := []*testStruct{
		&testStruct{"hello"},
		&testStruct{"goodbye"},
	}
	for y := range testStructs {
		_ = y
	}
}
`

	runner.Run(t, testPackage, func(t *testing.T, env *Env) {
		env.OpenFile("lib_test.go")
		env.Await(
			DiagnosticAt("lib_test.go", 10, 2),
			DiagnosticAt("lib_test.go", 11, 2),
		)
		env.OpenFile("lib.go")
		env.RegexpReplace("lib.go", "_ = x", "var y int")
		env.Await(
			env.DiagnosticAtRegexp("lib.go", "y int"),
			EmptyDiagnostics("lib_test.go"),
		)
	})
}

// Tests golang/go#38328.
func TestPackageChange_Issue38328(t *testing.T) {
	const packageChange = `
-- go.mod --
module fake
-- a.go --
package foo
func main() {}
`
	runner.Run(t, packageChange, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.RegexpReplace("a.go", "foo", "foox")
		env.Await(
			// When the bug reported in #38328 was present, we didn't get erroneous
			// file diagnostics until after the didChange message generated by the
			// package renaming was fully processed. Therefore, in order for this
			// test to actually exercise the bug, we must wait until that work has
			// completed.
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1),
				NoDiagnostics("a.go"),
			),
		)
	})
}

const testPackageWithRequire = `
-- go.mod --
module mod.com

go 1.12

require (
	foo.test v1.2.3
)
-- print.go --
package lib

import (
	"fmt"

	"foo.test/bar"
)

func PrintAnswer() {
	fmt.Printf("answer: %s", bar.Answer)
}
`

const testPackageWithRequireProxy = `
-- foo.test@v1.2.3/go.mod --
module foo.test

go 1.12
-- foo.test@v1.2.3/bar/const.go --
package bar

const Answer = 42
`

func TestResolveDiagnosticWithDownload(t *testing.T) {
	runner.Run(t, testPackageWithRequire, func(t *testing.T, env *Env) {
		env.OpenFile("print.go")
		// Check that gopackages correctly loaded this dependency. We should get a
		// diagnostic for the wrong formatting type.
		// TODO: we should be able to easily also match the diagnostic message.
		env.Await(env.DiagnosticAtRegexp("print.go", "fmt.Printf"))
	}, WithProxyFiles(testPackageWithRequireProxy))
}

func TestMissingDependency(t *testing.T) {
	runner.Run(t, testPackageWithRequire, func(t *testing.T, env *Env) {
		env.OpenFile("print.go")
		env.Await(LogMatching(protocol.Error, "initial workspace load failed", 1))
	})
}

// Tests golang/go#36951.
func TestAdHocPackages_Issue36951(t *testing.T) {
	const adHoc = `
-- b/b.go --
package b

func Hello() {
	var x int
}
`
	runner.Run(t, adHoc, func(t *testing.T, env *Env) {
		env.OpenFile("b/b.go")
		env.Await(env.DiagnosticAtRegexp("b/b.go", "x"))
	})
}

// Tests golang/go#37984: GOPATH should be read from the go command.
func TestNoGOPATH_Issue37984(t *testing.T) {
	const files = `
-- main.go --
package main

func _() {
	fmt.Println("Hello World")
}
`
	editorConfig := EditorConfig{Env: map[string]string{"GOPATH": ""}}
	withOptions(editorConfig).run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.Await(env.DiagnosticAtRegexp("main.go", "fmt"))
		env.SaveBuffer("main.go")
		env.Await(EmptyDiagnostics("main.go"))
	})
}

// Tests golang/go#38669.
func TestEqualInEnv_Issue38669(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

-- main.go --
package main

var _ = x.X
-- x/x.go --
package x

var X = 0
`
	editorConfig := EditorConfig{Env: map[string]string{"GOFLAGS": "-tags=foo"}}
	withOptions(editorConfig).run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.OrganizeImports("main.go")
		env.Await(EmptyDiagnostics("main.go"))
	})
}

// Tests golang/go#38467.
func TestNoSuggestedFixesForGeneratedFiles_Issue38467(t *testing.T) {
	const generated = `
-- go.mod --
module mod.com

-- main.go --
package main

// Code generated by generator.go. DO NOT EDIT.

func _() {
	for i, _ := range []string{} {
		_ = i
	}
}
`
	runner.Run(t, generated, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		original := env.ReadWorkspaceFile("main.go")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				DiagnosticAt("main.go", 5, 8),
				ReadDiagnostics("main.go", &d),
			),
		)
		// Apply fixes and save the buffer.
		env.ApplyQuickFixes("main.go", d.Diagnostics)
		env.SaveBuffer("main.go")
		fixed := env.ReadWorkspaceFile("main.go")
		if original != fixed {
			t.Fatalf("generated file was changed by quick fixes:\n%s", tests.Diff(original, fixed))
		}
	})
}

// Expect a module/GOPATH error if there is an error in the file at startup.
// Tests golang/go#37279.
func TestShowMessage_Issue37279(t *testing.T) {
	const noModule = `
-- a.go --
package foo

func f() {
	fmt.Printl()
}
`
	runner.Run(t, noModule, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.Await(env.DiagnosticAtRegexp("a.go", "fmt.Printl"), ShownMessage(""))
	})
}

// Expect no module/GOPATH error if there is no error in the file.
// Tests golang/go#37279.
func TestNoShowMessage_Issue37279(t *testing.T) {
	const noModule = `
-- a.go --
package foo

func f() {
}
`
	runner.Run(t, noModule, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1),
				NoDiagnostics("a.go"),
			),
			NoShowMessage(),
		)
		// introduce an error, expect no Show Message
		env.RegexpReplace("a.go", "func", "fun")
		env.Await(env.DiagnosticAtRegexp("a.go", "fun"), NoShowMessage())
	})
}

func TestNonGoFolder(t *testing.T) {
	const files = `
-- hello.txt --
hi mom
`
	for _, go111module := range []string{"on", "off", ""} {
		t.Run(fmt.Sprintf("GO111MODULE_%v", go111module), func(t *testing.T) {
			withOptions(EditorConfig{
				Env: map[string]string{"GO111MODULE": go111module},
			}).run(t, files, func(t *testing.T, env *Env) {
				env.OpenFile("hello.txt")
				env.Await(
					OnceMet(
						CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1),
						NoShowMessage(),
					),
				)
			})
		})
	}
}

// Tests golang/go#38602.
func TestNonexistentFileDiagnostics_Issue38602(t *testing.T) {
	const collision = `
-- x/x.go --
package x

import "x/hello"

func Hello() {
	hello.HiThere()
}
-- x/main.go --
package main

func main() {
	fmt.Println("")
}
`
	runner.Run(t, collision, func(t *testing.T, env *Env) {
		env.OpenFile("x/main.go")
		env.Await(
			env.DiagnosticAtRegexp("x/main.go", "fmt.Println"),
		)
		env.OrganizeImports("x/main.go")
		// span.Parse misparses the error message when multiple packages are
		// defined in the same directory, creating a garbage filename.
		// Previously, we would send diagnostics for this nonexistent file.
		// This test checks that we don't send diagnostics for this file.
		dir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		badFile := fmt.Sprintf("%s/found packages main (main.go) and x (x.go) in %s/src/x", dir, env.Sandbox.GOPATH())
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1),
				EmptyDiagnostics("x/main.go"),
			),
			NoDiagnostics(badFile),
		)
	}, InGOPATH())
}

const ardanLabsProxy = `
-- github.com/ardanlabs/conf@v1.2.3/go.mod --
module github.com/ardanlabs/conf

go 1.12
-- github.com/ardanlabs/conf@v1.2.3/conf.go --
package conf

var ErrHelpWanted error
`

// Test for golang/go#38211.
func Test_Issue38211(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)
	const ardanLabs = `
-- go.mod --
module mod.com

go 1.14
-- main.go --
package main

import "github.com/ardanlabs/conf"

func main() {
	_ = conf.ErrHelpWanted
}
`
	withOptions(
		WithProxyFiles(ardanLabsProxy),
	).run(t, ardanLabs, func(t *testing.T, env *Env) {
		// Expect a diagnostic with a suggested fix to add
		// "github.com/ardanlabs/conf" to the go.mod file.
		env.OpenFile("go.mod")
		env.OpenFile("main.go")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				env.DiagnosticAtRegexp("main.go", `"github.com/ardanlabs/conf"`),
				ReadDiagnostics("main.go", &d),
			),
		)
		env.ApplyQuickFixes("main.go", d.Diagnostics)
		env.SaveBuffer("go.mod")
		env.Await(
			EmptyDiagnostics("main.go"),
		)
		// Comment out the line that depends on conf and expect a
		// diagnostic and a fix to remove the import.
		env.RegexpReplace("main.go", "_ = conf.ErrHelpWanted", "//_ = conf.ErrHelpWanted")
		env.Await(
			env.DiagnosticAtRegexp("main.go", `"github.com/ardanlabs/conf"`),
		)
		env.SaveBuffer("main.go")
		// Expect a diagnostic and fix to remove the dependency in the go.mod.
		env.Await(EmptyDiagnostics("main.go"))
		env.Await(
			OnceMet(
				env.DiagnosticAtRegexp("go.mod", "require github.com/ardanlabs/conf"),
				ReadDiagnostics("go.mod", &d),
			),
		)
		env.ApplyQuickFixes("go.mod", d.Diagnostics)
		env.SaveBuffer("go.mod")
		env.Await(
			EmptyDiagnostics("go.mod"),
		)
		// Uncomment the lines and expect a new diagnostic for the import.
		env.RegexpReplace("main.go", "//_ = conf.ErrHelpWanted", "_ = conf.ErrHelpWanted")
		env.SaveBuffer("main.go")
		env.Await(
			env.DiagnosticAtRegexp("main.go", `"github.com/ardanlabs/conf"`),
		)
	})
}

// Test for golang/go#38207.
func TestNewModule_Issue38207(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)
	const emptyFile = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
`
	withOptions(
		WithProxyFiles(ardanLabsProxy),
	).run(t, emptyFile, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.OpenFile("go.mod")
		env.EditBuffer("main.go", fake.NewEdit(0, 0, 0, 0, `package main

import "github.com/ardanlabs/conf"

func main() {
	_ = conf.ErrHelpWanted
}
`))
		env.SaveBuffer("main.go")
		var d protocol.PublishDiagnosticsParams
		env.Await(
			OnceMet(
				env.DiagnosticAtRegexp("main.go", `"github.com/ardanlabs/conf"`),
				ReadDiagnostics("main.go", &d),
			),
		)
		env.ApplyQuickFixes("main.go", d.Diagnostics)
		env.Await(
			EmptyDiagnostics("main.go"),
		)
	})
}

// Test for golang/go#36960.
func TestNewFileBadImports_Issue36960(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)
	const simplePackage = `
-- go.mod --
module mod.com

go 1.14
-- a/a1.go --
package a

import "fmt"

func _() {
	fmt.Println("hi")
}
`
	runner.Run(t, simplePackage, func(t *testing.T, env *Env) {
		env.OpenFile("a/a1.go")
		env.CreateBuffer("a/a2.go", ``)
		if err := env.Editor.SaveBufferWithoutActions(env.Ctx, "a/a2.go"); err != nil {
			t.Fatal(err)
		}
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidSave), 1),
				NoDiagnostics("a/a1.go"),
			),
		)
		env.EditBuffer("a/a2.go", fake.NewEdit(0, 0, 0, 0, `package a`))
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1),
				NoDiagnostics("a/a1.go"),
			),
		)
	})
}

// This test tries to replicate the workflow of a user creating a new x test.
// It also tests golang/go#39315.
func TestManuallyCreatingXTest(t *testing.T) {
	// Only for 1.15 because of golang/go#37971.
	testenv.NeedsGo1Point(t, 15)

	// Create a package that already has a test variant (in-package test).
	const testVariant = `
-- go.mod --
module mod.com

go 1.15
-- hello/hello.go --
package hello

func Hello() {
	var x int
}
-- hello/hello_test.go --
package hello

import "testing"

func TestHello(t *testing.T) {
	var x int
	Hello()
}
`
	runner.Run(t, testVariant, func(t *testing.T, env *Env) {
		// Open the file, triggering the workspace load.
		// There are errors in the code to ensure all is working as expected.
		env.OpenFile("hello/hello.go")
		env.Await(
			env.DiagnosticAtRegexp("hello/hello.go", "x"),
			env.DiagnosticAtRegexp("hello/hello_test.go", "x"),
		)

		// Create an empty file with the intention of making it an x test.
		// This resembles a typical flow in an editor like VS Code, in which
		// a user would create an empty file and add content, saving
		// intermittently.
		// TODO(rstambler): There might be more edge cases here, as file
		// content can be added incrementally.
		env.CreateBuffer("hello/hello_x_test.go", ``)

		// Save the empty file (no actions since formatting will fail).
		env.Editor.SaveBufferWithoutActions(env.Ctx, "hello/hello_x_test.go")

		// Add the content. The missing import is for the package under test.
		env.EditBuffer("hello/hello_x_test.go", fake.NewEdit(0, 0, 0, 0, `package hello_test

import (
	"testing"
)

func TestHello(t *testing.T) {
	hello.Hello()
}
`))
		// Expect a diagnostic for the missing import. Save, which should
		// trigger import organization. The diagnostic should clear.
		env.Await(
			env.DiagnosticAtRegexp("hello/hello_x_test.go", "hello.Hello"),
		)
		env.SaveBuffer("hello/hello_x_test.go")
		env.Await(
			EmptyDiagnostics("hello/hello_x_test.go"),
		)
	})
}

// Reproduce golang/go#40690.
func TestCreateOnlyXTest(t *testing.T) {
	t.Skip("golang/go#40690 is not resolved yet.")

	const mod = `
	-- go.mod --
	module mod.com
	-- foo/foo.go --
	package foo
	-- foo/bar_test.go --
	`
	run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("foo/bar_test.go")
		env.EditBuffer("foo/bar_test.go", fake.NewEdit(0, 0, 0, 0, `package foo
	`))
		env.Await(
			CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1),
		)
		env.RegexpReplace("foo/bar_test.go", "package foo", "package foo_test")
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 2),
				NoErrorLogs(),
			),
		)
	})
}

func TestChangePackageName(t *testing.T) {
	t.Skip("This issue hasn't been fixed yet. See golang.org/issue/41061.")

	const mod = `
-- go.mod --
module mod.com
-- foo/foo.go --
package foo
-- foo/bar_test.go --
package foo_
`
	run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("foo/bar_test.go")
		env.RegexpReplace("foo/bar_test.go", "package foo_", "package foo_test")
		env.SaveBuffer("foo/bar_test.go")
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidSave), 1),
				NoDiagnostics("foo/bar_test.go"),
			),
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidSave), 1),
				NoDiagnostics("foo/foo.go"),
			),
		)
	})
}

// Reproduces golang/go#40825.
func TestEmptyGOPATHXTest_40825(t *testing.T) {
	const files = `
-- x.go --
package x
-- x_test.go --
`

	withOptions(InGOPATH()).run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("x_test.go")
		env.EditBuffer("x_test.go", fake.NewEdit(0, 0, 0, 0, "pack"))
		env.Await(
			CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1),
			NoShowMessage(),
		)
	})
}

func TestIgnoredFiles(t *testing.T) {
	const ws = `
-- go.mod --
module mod.com

go 1.15
-- _foo/x.go --
package x

var _ = foo.Bar
`
	runner.Run(t, ws, func(t *testing.T, env *Env) {
		env.OpenFile("_foo/x.go")
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1),
				NoDiagnostics("_foo/x.go"),
			))
	})
}

// Partially reproduces golang/go#38977, moving a file between packages.
// It also gets hit by some go command bug fixed in 1.15, but we don't
// care about that so much here.
func TestDeletePackage(t *testing.T) {
	const ws = `
-- go.mod --
module mod.com

go 1.15
-- a/a.go --
package a

const A = 1

-- b/b.go --
package b

import "mod.com/a"

const B = a.A

-- c/c.go --
package c

import "mod.com/a"

const C = a.A
`
	runner.Run(t, ws, func(t *testing.T, env *Env) {
		env.OpenFile("b/b.go")
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1))
		// Delete c/c.go, the only file in package c.
		env.RemoveWorkspaceFile("c/c.go")

		// We should still get diagnostics for files that exist.
		env.RegexpReplace("b/b.go", `a.A`, "a.Nonexistant")
		env.Await(env.DiagnosticAtRegexp("b/b.go", `Nonexistant`))
	})
}

// This is a copy of the scenario_default/quickfix_empty_files.txt test from
// govim. Reproduces golang/go#39646.
func TestQuickFixEmptyFiles(t *testing.T) {
	testenv.NeedsGo1Point(t, 15)

	const mod = `
-- go.mod --
module mod.com

go 1.12
`
	// To fully recreate the govim tests, we create files by inserting
	// a newline, adding to the file, and then deleting the newline.
	// Wait for each event to process to avoid cancellations and force
	// package loads.
	writeGoVim := func(env *Env, name, content string) {
		env.WriteWorkspaceFile(name, "")
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChangeWatchedFiles), 1))

		env.CreateBuffer(name, "\n")
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1))

		env.EditBuffer(name, fake.NewEdit(1, 0, 1, 0, content))
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1))

		env.EditBuffer(name, fake.NewEdit(0, 0, 1, 0, ""))
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1))
	}

	const p = `package p; func DoIt(s string) {};`
	const main = `package main

import "mod.com/p"

func main() {
	p.DoIt(5)
}
`
	// A simple version of the test that reproduces most of the problems it
	// exposes.
	t.Run("short", func(t *testing.T) {
		runner.Run(t, mod, func(t *testing.T, env *Env) {
			writeGoVim(env, "p/p.go", p)
			writeGoVim(env, "main.go", main)
			env.Await(env.DiagnosticAtRegexp("main.go", "5"))
		})
	})

	// A full version that replicates the whole flow of the test.
	t.Run("full", func(t *testing.T) {
		runner.Run(t, mod, func(t *testing.T, env *Env) {
			writeGoVim(env, "p/p.go", p)
			writeGoVim(env, "main.go", main)
			writeGoVim(env, "p/p_test.go", `package p

import "testing"

func TestDoIt(t *testing.T) {
	DoIt(5)
}
`)
			writeGoVim(env, "p/x_test.go", `package p_test

import (
	"testing"

	"mod.com/p"
)

func TestDoIt(t *testing.T) {
	p.DoIt(5)
}
`)
			env.Await(
				env.DiagnosticAtRegexp("main.go", "5"),
				env.DiagnosticAtRegexp("p/p_test.go", "5"),
				env.DiagnosticAtRegexp("p/x_test.go", "5"),
			)
			env.RegexpReplace("p/p.go", "s string", "i int")
			env.Await(
				EmptyDiagnostics("main.go"),
				EmptyDiagnostics("p/p_test.go"),
				EmptyDiagnostics("p/x_test.go"),
			)
		})
	})
}

func TestSingleFile(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com

go 1.13
-- a/a.go --
package a

func _() {
	var x int
}
`
	runner.Run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.Await(
			env.DiagnosticAtRegexp("a/a.go", "x"),
		)
	}, WithoutWorkspaceFolders())
}

// Reproduces the case described in
// https://github.com/golang/go/issues/39296#issuecomment-652058883.
func TestPkgm(t *testing.T) {
	const basic = `
-- go.mod --
module mod.com

go 1.15
-- foo/foo.go --
package foo

import "fmt"

func Foo() {
	fmt.Println("")
}
`
	runner.Run(t, basic, func(t *testing.T, env *Env) {
		testenv.NeedsGo1Point(t, 15)

		env.WriteWorkspaceFile("foo/foo_test.go", `package main

func main() {

}`)
		env.OpenFile("foo/foo_test.go")
		env.RegexpReplace("foo/foo_test.go", `package main`, `package foo`)
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1),
				NoDiagnostics("foo/foo.go"),
			),
		)
	})
}

func TestClosingBuffer(t *testing.T) {
	const basic = `
-- go.mod --
module mod.com

go 1.14
-- main.go --
package main

func main() {}
`
	runner.Run(t, basic, func(t *testing.T, env *Env) {
		env.Editor.CreateBuffer(env.Ctx, "foo.go", `package main`)
		env.Await(
			CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1),
		)
		env.CloseBuffer("foo.go")
		env.Await(
			OnceMet(
				CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidClose), 1),
				NoLogMatching(protocol.Info, "packages=0"),
			),
		)
	})
}

// Reproduces golang/go#38424.
func TestCutAndPaste(t *testing.T) {
	const basic = `
-- go.mod --
module mod.com

go 1.14
-- main2.go --
package main
`
	runner.Run(t, basic, func(t *testing.T, env *Env) {
		env.CreateBuffer("main.go", "")
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidOpen), 1))

		env.Editor.SaveBufferWithoutActions(env.Ctx, "main.go")
		env.Await(
			CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidSave), 1),
			CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChangeWatchedFiles), 1),
		)

		env.EditBuffer("main.go", fake.NewEdit(0, 0, 0, 0, `package main

func main() {
}
`))
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 1))

		env.SaveBuffer("main.go")
		env.Await(
			CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidSave), 2),
			CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChangeWatchedFiles), 2),
		)

		env.EditBuffer("main.go", fake.NewEdit(0, 0, 4, 0, ""))
		env.Await(CompletedWork(lsp.DiagnosticWorkTitle(lsp.FromDidChange), 2))

		env.EditBuffer("main.go", fake.NewEdit(0, 0, 0, 0, `package main

func main() {
	var x int
}
`))
		env.Await(
			env.DiagnosticAtRegexp("main.go", "x"),
		)
	})
}

// Reproduces golang/go#39763.
func TestInvalidPackageName(t *testing.T) {
	testenv.NeedsGo1Point(t, 15)

	const pkgDefault = `
-- go.mod --
module mod.com
-- main.go --
package default

func main() {}
`
	runner.Run(t, pkgDefault, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.Await(
			env.DiagnosticAtRegexp("main.go", "default"),
		)
	})
}

// This tests the functionality of the "limitWorkspaceScope"
func TestLimitWorkspaceScope(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com
-- a/main.go --
package main

func main() {}
-- main.go --
package main

func main() {
	var x int
}
`
	withOptions(WithRootPath("a")).run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("a/main.go")
		env.Await(
			env.DiagnosticAtRegexp("main.go", "x"),
		)
	})
	withOptions(WithRootPath("a"), WithLimitWorkspaceScope()).run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("a/main.go")
		env.Await(
			NoDiagnostics("main.go"),
		)
	})
}

func TestStaticcheckDiagnostic(t *testing.T) {
	const files = `
-- go.mod --
module mod.com
-- main.go --
package main

import "fmt"

type t struct {
	msg string
}

func main() {
	x := []t{t{"msg"}}
	fmt.Println(x)
}
`

	withOptions(
		EditorConfig{EnableStaticcheck: true},
	).run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		// Staticcheck should generate a diagnostic to simplify this literal.
		env.Await(env.DiagnosticAtRegexp("main.go", `t{"msg"}`))
	})
}

// Test some secondary diagnostics
func TestSecondaryDiagnostics(t *testing.T) {
	const dir = `
-- go.mod --
module mod.com
-- main.go --
package main
func main() {
	panic("not here")
}
-- other.go --
package main
func main() {}
`
	runner.Run(t, dir, func(t *testing.T, env *Env) {
		log.SetFlags(log.Lshortfile)
		env.OpenFile("main.go")
		env.OpenFile("other.go")
		x := env.DiagnosticsFor("main.go")
		if x == nil {
			t.Fatalf("expected 1 diagnostic, got none")
		}
		if len(x.Diagnostics) != 1 {
			t.Fatalf("main.go, got %d diagnostics, expected 1", len(x.Diagnostics))
		}
		keep := x.Diagnostics[0]
		y := env.DiagnosticsFor("other.go")
		if len(y.Diagnostics) != 1 {
			t.Fatalf("other.go: got %d diagnostics, expected 1", len(y.Diagnostics))
		}
		if len(y.Diagnostics[0].RelatedInformation) != 1 {
			t.Fatalf("got %d RelatedInformations, expected 1", len(y.Diagnostics[0].RelatedInformation))
		}
		// check that the RelatedInformation matches the error from main.go
		c := y.Diagnostics[0].RelatedInformation[0]
		if c.Location.Range != keep.Range {
			t.Errorf("locations don't match. Got %v expected %v", c.Location.Range, keep.Range)
		}
	})
}

func TestNotifyOrphanedFiles(t *testing.T) {
	// Need GO111MODULE=on for this test to work with Go 1.12.
	testenv.NeedsGo1Point(t, 13)

	const files = `
-- go.mod --
module mod.com

go 1.12
-- a/a.go --
package a

func main() {
	var x int
}
-- a/a_ignore.go --
// +build ignore

package a

func _() {
	var x int
}
`
	run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.Await(
			env.DiagnosticAtRegexp("a/a.go", "x"),
		)
		env.OpenFile("a/a_ignore.go")
		env.Await(
			DiagnosticAt("a/a_ignore.go", 2, 8),
		)
	})
}

func TestEnableAllExperiments(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com

-- main.go --
package main

import "bytes"

func b(c bytes.Buffer) {
	_ = 1
}
`
	withOptions(
		EditorConfig{
			AllExperiments: true,
		},
	).run(t, mod, func(t *testing.T, env *Env) {
		// Confirm that the setting doesn't cause any warnings.
		env.Await(NoShowMessage())
	})
}

func TestSwig(t *testing.T) {
	t.Skipf("skipped until golang/go#37098 is resolved")

	const mod = `
-- go.mod --
module mod.com
-- pkg/simple/export_swig.go --
package simple

func ExportSimple(x, y int) int {
	return Gcd(x, y)
}
-- pkg/simple/simple.swigcxx --
%module simple

%inline %{
extern int gcd(int x, int y)
{
  int g;
  g = y;
  while (x > 0) {
    g = x;
    x = y % x;
    y = g;
  }
  return g;
}
%}
-- main.go --
package a

func main() {
	var x int
}
`
	run(t, mod, func(t *testing.T, env *Env) {
		env.Await(
			OnceMet(
				InitialWorkspaceLoad,
				NoDiagnosticWithMessage("illegal character U+0023 '#'"),
			),
		)
	})
}

// When foo_test.go is opened, gopls will object to the borked package name.
// This test asserts that when the package name is fixed, gopls will soon after
// have no more complaints about it.
// https://github.com/golang/go/issues/41061
func TestRenamePackage(t *testing.T) {
	t.Skip("Waiting for the fix that makes this pass: https://github.com/golang/go/issues/41061")

	const contents = `
-- go.mod --
module mod.com
-- foo.go --
package foo
-- foo_test.go --
package foo_`

	runner.Run(t, contents, func(t *testing.T, env *Env) {
		env.OpenFile("foo_test.go")
		env.RegexpReplace("foo_test.go", "foo_", "foo_test")
		env.SaveBuffer("foo_test.go")
		env.Await(
			EmptyDiagnostics("foo_test.go"),
		)
	})
}

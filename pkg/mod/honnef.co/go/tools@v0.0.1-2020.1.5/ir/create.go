// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

// This file implements the CREATE phase of IR construction.
// See builder.go for explanation.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sync"

	"golang.org/x/tools/go/types/typeutil"
)

// NewProgram returns a new IR Program.
//
// mode controls diagnostics and checking during IR construction.
//
func NewProgram(fset *token.FileSet, mode BuilderMode) *Program {
	prog := &Program{
		Fset:     fset,
		imported: make(map[string]*Package),
		packages: make(map[*types.Package]*Package),
		thunks:   make(map[selectionKey]*Function),
		bounds:   make(map[*types.Func]*Function),
		mode:     mode,
	}

	h := typeutil.MakeHasher() // protected by methodsMu, in effect
	prog.methodSets.SetHasher(h)
	prog.canon.SetHasher(h)

	return prog
}

// memberFromObject populates package pkg with a member for the
// typechecker object obj.
//
// For objects from Go source code, syntax is the associated syntax
// tree (for funcs and vars only); it will be used during the build
// phase.
//
func memberFromObject(pkg *Package, obj types.Object, syntax ast.Node) {
	name := obj.Name()
	switch obj := obj.(type) {
	case *types.Builtin:
		if pkg.Pkg != types.Unsafe {
			panic("unexpected builtin object: " + obj.String())
		}

	case *types.TypeName:
		pkg.Members[name] = &Type{
			object: obj,
			pkg:    pkg,
		}

	case *types.Const:
		c := &NamedConst{
			object: obj,
			Value:  NewConst(obj.Val(), obj.Type()),
			pkg:    pkg,
		}
		pkg.values[obj] = c.Value
		pkg.Members[name] = c

	case *types.Var:
		g := &Global{
			Pkg:    pkg,
			name:   name,
			object: obj,
			typ:    types.NewPointer(obj.Type()), // address
		}
		pkg.values[obj] = g
		pkg.Members[name] = g

	case *types.Func:
		sig := obj.Type().(*types.Signature)
		if sig.Recv() == nil && name == "init" {
			pkg.ninit++
			name = fmt.Sprintf("init#%d", pkg.ninit)
		}
		fn := &Function{
			name:      name,
			object:    obj,
			Signature: sig,
			Pkg:       pkg,
			Prog:      pkg.Prog,
		}

		fn.source = syntax
		fn.initHTML(pkg.printFunc)
		if syntax == nil {
			fn.Synthetic = "loaded from gc object file"
		} else {
			fn.functionBody = new(functionBody)
		}

		pkg.values[obj] = fn
		pkg.Functions = append(pkg.Functions, fn)
		if sig.Recv() == nil {
			pkg.Members[name] = fn // package-level function
		}

	default: // (incl. *types.Package)
		panic("unexpected Object type: " + obj.String())
	}
}

// membersFromDecl populates package pkg with members for each
// typechecker object (var, func, const or type) associated with the
// specified decl.
//
func membersFromDecl(pkg *Package, decl ast.Decl) {
	switch decl := decl.(type) {
	case *ast.GenDecl: // import, const, type or var
		switch decl.Tok {
		case token.CONST:
			for _, spec := range decl.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					if !isBlankIdent(id) {
						memberFromObject(pkg, pkg.info.Defs[id], nil)
					}
				}
			}

		case token.VAR:
			for _, spec := range decl.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					if !isBlankIdent(id) {
						memberFromObject(pkg, pkg.info.Defs[id], spec)
					}
				}
			}

		case token.TYPE:
			for _, spec := range decl.Specs {
				id := spec.(*ast.TypeSpec).Name
				if !isBlankIdent(id) {
					memberFromObject(pkg, pkg.info.Defs[id], nil)
				}
			}
		}

	case *ast.FuncDecl:
		id := decl.Name
		if !isBlankIdent(id) {
			memberFromObject(pkg, pkg.info.Defs[id], decl)
		}
	}
}

// CreatePackage constructs and returns an IR Package from the
// specified type-checked, error-free file ASTs, and populates its
// Members mapping.
//
// importable determines whether this package should be returned by a
// subsequent call to ImportedPackage(pkg.Path()).
//
// The real work of building IR form for each function is not done
// until a subsequent call to Package.Build().
//
func (prog *Program) CreatePackage(pkg *types.Package, files []*ast.File, info *types.Info, importable bool) *Package {
	p := &Package{
		Prog:      prog,
		Members:   make(map[string]Member),
		values:    make(map[types.Object]Value),
		Pkg:       pkg,
		info:      info,  // transient (CREATE and BUILD phases)
		files:     files, // transient (CREATE and BUILD phases)
		printFunc: prog.PrintFunc,
	}

	// Add init() function.
	p.init = &Function{
		name:         "init",
		Signature:    new(types.Signature),
		Synthetic:    "package initializer",
		Pkg:          p,
		Prog:         prog,
		functionBody: new(functionBody),
	}
	p.init.initHTML(prog.PrintFunc)
	p.Members[p.init.name] = p.init
	p.Functions = append(p.Functions, p.init)

	// CREATE phase.
	// Allocate all package members: vars, funcs, consts and types.
	if len(files) > 0 {
		// Go source package.
		for _, file := range files {
			for _, decl := range file.Decls {
				membersFromDecl(p, decl)
			}
		}
	} else {
		// GC-compiled binary package (or "unsafe")
		// No code.
		// No position information.
		scope := p.Pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			memberFromObject(p, obj, nil)
			if obj, ok := obj.(*types.TypeName); ok {
				if named, ok := obj.Type().(*types.Named); ok {
					for i, n := 0, named.NumMethods(); i < n; i++ {
						memberFromObject(p, named.Method(i), nil)
					}
				}
			}
		}
	}

	// Add initializer guard variable.
	initguard := &Global{
		Pkg:  p,
		name: "init$guard",
		typ:  types.NewPointer(tBool),
	}
	p.Members[initguard.Name()] = initguard

	if prog.mode&GlobalDebug != 0 {
		p.SetDebugMode(true)
	}

	if prog.mode&PrintPackages != 0 {
		printMu.Lock()
		p.WriteTo(os.Stdout)
		printMu.Unlock()
	}

	if importable {
		prog.imported[p.Pkg.Path()] = p
	}
	prog.packages[p.Pkg] = p

	return p
}

// printMu serializes printing of Packages/Functions to stdout.
var printMu sync.Mutex

// AllPackages returns a new slice containing all packages in the
// program prog in unspecified order.
//
func (prog *Program) AllPackages() []*Package {
	pkgs := make([]*Package, 0, len(prog.packages))
	for _, pkg := range prog.packages {
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

// ImportedPackage returns the importable Package whose PkgPath
// is path, or nil if no such Package has been created.
//
// A parameter to CreatePackage determines whether a package should be
// considered importable. For example, no import declaration can resolve
// to the ad-hoc main package created by 'go build foo.go'.
//
// TODO(adonovan): rethink this function and the "importable" concept;
// most packages are importable. This function assumes that all
// types.Package.Path values are unique within the ir.Program, which is
// false---yet this function remains very convenient.
// Clients should use (*Program).Package instead where possible.
// IR doesn't really need a string-keyed map of packages.
//
func (prog *Program) ImportedPackage(path string) *Package {
	return prog.imported[path]
}

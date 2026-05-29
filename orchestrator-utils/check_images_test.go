package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestCheckImagesExposesAndUsesBuildBranch(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), "main.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	checkImages := findFunc(file, "CheckImages")
	if checkImages == nil {
		t.Fatal("CheckImages function not found")
	}

	if !hasParam(checkImages, "buildBranch") {
		t.Fatal("CheckImages should expose a buildBranch parameter")
	}

	if !callsGetLastCommitShaWithRef(checkImages.Body, "buildBranch") {
		t.Fatal("CheckImages should pass buildBranch to GetLastCommitSha")
	}
}

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func hasParam(fn *ast.FuncDecl, name string) bool {
	for _, field := range fn.Type.Params.List {
		for _, paramName := range field.Names {
			if paramName.Name == name {
				return true
			}
		}
	}
	return false
}

func callsGetLastCommitShaWithRef(body *ast.BlockStmt, ref string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 4 {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "GetLastCommitSha" {
			return true
		}

		arg, ok := call.Args[3].(*ast.Ident)
		if ok && arg.Name == ref {
			found = true
			return false
		}

		return true
	})
	return found
}

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestGetLastCommitShaIgnoresVersionBumpCommits(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), "main.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	fn := findFunc(file, "GetLastCommitSha")
	if fn == nil {
		t.Fatal("GetLastCommitSha function not found")
	}

	if !stringLiteralInFunc(fn, "--grep=^Bump versão para ") {
		t.Fatal("GetLastCommitSha should ignore automatic version bump commits")
	}
}

func stringLiteralInFunc(fn *ast.FuncDecl, value string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if found {
			return false
		}

		lit, ok := node.(*ast.BasicLit)
		if ok && lit.Value == `"`+value+`"` {
			found = true
			return false
		}

		return true
	})
	return found
}

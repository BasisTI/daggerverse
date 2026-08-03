package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Promote roda uma vez por merge em main e itera sobre TODAS as imagens do
// projeto, não só as que mudaram. Sem a comparação de digests ele recopia tags
// de produção idênticas a cada promoção — foi o que aconteceu na licitacao, onde
// 6 de 10 imagens eram recópias.
func TestPromoteSkipsImagesAlreadyPromoted(t *testing.T) {
	promote := parseFunc(t, "Promote")

	src, dst := digestArgs(promote.Body)
	if src != "srcImageRef" {
		t.Errorf("Promote deveria consultar o digest de srcImageRef, consultou %q", src)
	}
	if dst != "dstImageRef" {
		t.Errorf("Promote deveria consultar o digest de dstImageRef, consultou %q", dst)
	}

	if !skipsOnDigestMatch(promote.Body) {
		t.Error("Promote deveria pular a cópia quando os digests coincidem")
	}
}

// A comparação decide com base no registry, então o resultado do `crane digest`
// não pode vir do cache de exec de uma execução anterior.
func TestPromoteBustsExecCache(t *testing.T) {
	promote := parseFunc(t, "Promote")

	if !mentions(promote.Body, "CACHE_BUSTER") {
		t.Error("o container crane do Promote deveria definir CACHE_BUSTER")
	}
}

func parseFunc(t *testing.T, name string) *ast.FuncDecl {
	t.Helper()

	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), "main.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	fn := findFunc(file, name)
	if fn == nil {
		t.Fatalf("função %s não encontrada", name)
	}
	return fn
}

// digestArgs devolve, na ordem em que aparecem, os identificadores passados às
// duas primeiras chamadas de imageDigest.
func digestArgs(body *ast.BlockStmt) (string, string) {
	var args []string

	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		fn, ok := call.Fun.(*ast.Ident)
		if !ok || fn.Name != "imageDigest" || len(call.Args) != 3 {
			return true
		}

		if ref, ok := call.Args[2].(*ast.Ident); ok {
			args = append(args, ref.Name)
		}
		return true
	})

	for len(args) < 2 {
		args = append(args, "")
	}
	return args[0], args[1]
}

// skipsOnDigestMatch exige que o `continue` esteja guardado por uma condição que
// consulta o digest — o corpo do Promote já tinha outro `continue` (o do label de
// versão ausente), que sozinho não prova nada sobre esta verificação.
func skipsOnDigestMatch(body *ast.BlockStmt) bool {
	found := false

	ast.Inspect(body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok || !mentionsDigest(stmt.Cond) {
			return !found
		}

		for _, inner := range stmt.Body.List {
			branch, ok := inner.(*ast.BranchStmt)
			if ok && branch.Tok == token.CONTINUE {
				found = true
			}
		}
		return !found
	})

	return found
}

// mentionsDigest reconhece tanto a chamada direta a imageDigest quanto a
// comparação com uma variável que guardou o resultado dela.
func mentionsDigest(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(node ast.Node) bool {
		switch expr := node.(type) {
		case *ast.CallExpr:
			if fn, ok := expr.Fun.(*ast.Ident); ok && fn.Name == "imageDigest" {
				found = true
			}
		case *ast.Ident:
			if strings.Contains(strings.ToLower(expr.Name), "digest") {
				found = true
			}
		}
		return !found
	})
	return found
}

func mentions(body *ast.BlockStmt, literal string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		basic, ok := node.(*ast.BasicLit)
		if ok && basic.Kind == token.STRING && basic.Value == `"`+literal+`"` {
			found = true
		}
		return !found
	})
	return found
}

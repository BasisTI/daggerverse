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

// TestPromoteFailsOnMissingImage trava o comportamento que deixou uma promoção
// errada passar despercebida: ao não conseguir ler o label de versão, Promote
// imprimia um aviso e seguia para a próxima imagem, terminando o job em verde
// sem ter promovido nada.
func TestPromoteFailsOnMissingImage(t *testing.T) {
	promote := parseFunc(t, "Promote")

	branch := versionLabelErrorBranch(promote.Body)
	if branch == nil {
		t.Fatal("Promote deveria checar o erro da leitura do label de versão")
	}
	if !returnsError(branch) {
		t.Error("Promote deveria devolver erro quando o label de versão não pode ser lido, não seguir para a próxima imagem")
	}
}

// TestImageResolutionUsesAllTriggerPaths garante que quem resolve a imagem no
// registry receba a lista inteira de paths do target. Resolver por um
// subconjunto faz o `sha-` cair num build anterior — foi assim que a
// lightdash-content foi promovida com uma versão defasada.
func TestImageResolutionUsesAllTriggerPaths(t *testing.T) {
	for _, name := range []string{"CheckImages", "Promote"} {
		t.Run(name, func(t *testing.T) {
			if !passesIdentToGetLastCommitSha(parseFunc(t, name).Body, "repoPaths") {
				t.Errorf("%s deveria passar a lista completa de trigger paths ao GetLastCommitSha", name)
			}
		})
	}

	if !hasParam(parseFunc(t, "GetLastCommitSha"), "paths") {
		t.Error("GetLastCommitSha deveria receber uma lista de paths, não um path só")
	}
}

// TestPromoteReturnsPromotedRefs trava o contrato usado pelo relatório na MR: o
// que Promote devolve é a lista das refs de DESTINO copiadas. Devolver a ref de
// origem (`sha-...`) ou o nome da imagem faria o comentário mostrar algo que não
// é a tag que está em produção.
func TestPromoteReturnsPromotedRefs(t *testing.T) {
	promote := parseFunc(t, "Promote")

	results := promote.Type.Results
	if results == nil || len(results.List) != 2 {
		t.Fatalf("Promote deveria devolver (string, error), devolve %d resultado(s)", numResults(results))
	}
	if name, ok := results.List[0].Type.(*ast.Ident); !ok || name.Name != "string" {
		t.Errorf("o primeiro resultado de Promote deveria ser string")
	}

	if !accumulatesIdent(promote.Body, "dstImageRef") {
		t.Error("Promote deveria acumular dstImageRef na lista devolvida")
	}
}

func numResults(results *ast.FieldList) int {
	if results == nil {
		return 0
	}
	return len(results.List)
}

// accumulatesIdent procura uma chamada `.WriteString(...)` que mencione o
// identificador, direta ou concatenado.
func accumulatesIdent(body *ast.BlockStmt, ident string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "WriteString" {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(inner ast.Node) bool {
				if name, ok := inner.(*ast.Ident); ok && name.Name == ident {
					found = true
				}
				return !found
			})
		}
		return !found
	})
	return found
}

// versionLabelErrorBranch devolve o `if` que trata o erro da leitura do label,
// identificado por ser o primeiro `if` após a chamada a Label.
func versionLabelErrorBranch(body *ast.BlockStmt) *ast.IfStmt {
	var branch *ast.IfStmt
	seenLabel := false

	ast.Inspect(body, func(node ast.Node) bool {
		if branch != nil {
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Label" {
				seenLabel = true
			}
			return true
		}
		if stmt, ok := node.(*ast.IfStmt); ok && seenLabel {
			branch = stmt
			return false
		}
		return true
	})
	return branch
}

func returnsError(branch *ast.IfStmt) bool {
	found := false
	ast.Inspect(branch.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.ReturnStmt); ok {
			found = true
		}
		return !found
	})
	return found
}

func passesIdentToGetLastCommitSha(body *ast.BlockStmt, ident string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "GetLastCommitSha" {
			return true
		}
		for _, arg := range call.Args {
			if name, ok := arg.(*ast.Ident); ok && name.Name == ident {
				found = true
				return false
			}
		}
		return true
	})
	return found
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
// consulta o digest. A checagem da condição continua valendo a pena mesmo agora
// que este é o único `continue` do laço: é ela que distingue "já promovida" de
// um `continue` qualquer que voltasse a aparecer.
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

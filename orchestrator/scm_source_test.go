package main

import (
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
)

// TestQualityStrategiesReceiveRepoRoot trava a regressão que reprovou a MR 584 do licitacao.
//
// O contrato do pipeline.CheckQuality é entregar a RAIZ DO REPOSITÓRIO à estratégia, com o
// subdiretório do target vindo à parte como `sourcePath`. O checkUv desrespeitava isso: recortava
// `source.Directory(sourcePath)` e passava o recorte ao módulo, deixando o .git para trás.
//
// A consequência não é sutil, mas é silenciosa: sem SCM o scanner não sabe quais linhas a merge
// request mudou e marca o projeto inteiro como código novo. Na MR 584 isso virou 126 violações
// -- 111 delas em arquivos que a MR nunca tocou --, 9,15% de duplicação e 0% de hotspots
// revisados, todas as quatro condições do quality gate vermelhas por débito histórico. O build
// tinha passado.
//
// As estratégias são closures que só rodam com um engine Dagger, então a asserção é sobre a
// árvore sintática, no mesmo espírito do TestQualityStrategiesUseResolvedSonarKey.
func TestQualityStrategiesReceiveRepoRoot(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "strategies.go", nil, 0)
	if err != nil {
		t.Fatalf("parse strategies.go: %v", err)
	}

	for _, fn := range []string{"checkMaven", "checkNpm", "checkUv"} {
		t.Run(fn, func(t *testing.T) {
			decl := findFunc(file, fn)
			if decl == nil {
				t.Fatalf("função %s não encontrada em strategies.go", fn)
			}

			var body strings.Builder
			if err := printer.Fprint(&body, fset, decl); err != nil {
				t.Fatalf("imprimir %s: %v", fn, err)
			}
			got := body.String()

			// Recortar o subdiretório do `source` é exatamente o bug: o que sobra não tem .git.
			// O recorte legítimo é o do módulo, informado por path (ModulePath/ModulePath-like),
			// nunca por Directory() sobre o argumento que a estratégia recebeu.
			if strings.Contains(got, "source.Directory(") {
				t.Errorf("%s recorta o source e perde o .git -- o subdiretório deve ir por path:\n%s", fn, got)
			}
		})
	}
}

// TestCheckUvInformsModulePath garante o outro lado do contrato: entregar a raiz só ajuda se o
// módulo souber qual subdiretório analisar. Sem o ModulePath o scanner rodaria da raiz do
// repositório e analisaria os outros targets junto.
func TestCheckUvInformsModulePath(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "strategies.go", nil, 0)
	if err != nil {
		t.Fatalf("parse strategies.go: %v", err)
	}

	decl := findFunc(file, "checkUv")
	if decl == nil {
		t.Fatal("checkUv não encontrada em strategies.go")
	}

	var body strings.Builder
	if err := printer.Fprint(&body, fset, decl); err != nil {
		t.Fatalf("imprimir checkUv: %v", err)
	}
	got := body.String()

	if !strings.Contains(got, "ModulePath = moduleSubpath(sourcePath)") {
		t.Errorf("checkUv não informa o ModulePath ao módulo uv:\n%s", got)
	}
}

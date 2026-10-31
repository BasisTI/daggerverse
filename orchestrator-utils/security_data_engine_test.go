package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"dagger/orchestrator-utils/internal/dagger"
)

// Estes testes montam a "imagem de dados" no próprio engine, sem registro, e passam pelo mesmo
// miolo que SecurityData usa depois do pull. Só rodam com TG368_ENGINE_TESTS=1:
//
//	TG368_ENGINE_TESTS=1 dagger run go test -run TestEngine ./...
//
// A imagem real (odc-data:13) NÃO é usada: ela só existe depois do bootstrap do projeto nvd-data.

func engineTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TG368_ENGINE_TESTS") != "1" {
		t.Skip("defina TG368_ENGINE_TESTS=1 para rodar contra o engine do Dagger")
	}
}

func dataImage(created string) *dagger.Container {
	ctr := dag.Container().WithDirectory(SecurityDataDir, dag.Directory().WithNewFile("odc.mv.db", "base-de-teste"))
	if created != "" {
		ctr = ctr.WithLabel(SecurityDataLabel, created)
	}
	return ctr
}

func TestEngineSecurityDataRecente(t *testing.T) {
	engineTest(t)
	ctx := context.Background()
	now := time.Now()
	dir, err := securityDataFromContainer(ctx, "teste", dataImage(now.Add(-time.Hour).Format(time.RFC3339)), now, 48, 168)
	if err != nil {
		t.Fatalf("base recente deveria passar: %v", err)
	}
	got, err := dir.File("odc.mv.db").Contents(ctx)
	if err != nil || got != "base-de-teste" {
		t.Errorf("conteúdo = %q, erro %v", got, err)
	}
}

func TestEngineSecurityDataComAvisoAindaPassa(t *testing.T) {
	engineTest(t)
	now := time.Now()
	if _, err := securityDataFromContainer(context.Background(), "teste", dataImage(now.Add(-72*time.Hour).Format(time.RFC3339)), now, 48, 168); err != nil {
		t.Fatalf("base com 72h só avisa: %v", err)
	}
}

func TestEngineSecurityDataComOitoDiasFalha(t *testing.T) {
	engineTest(t)
	now := time.Now()
	_, err := securityDataFromContainer(context.Background(), "teste", dataImage(now.Add(-8*24*time.Hour).Format(time.RFC3339)), now, 48, 168)
	if err == nil || !strings.Contains(err.Error(), "falha técnica") {
		t.Fatalf("base com 8 dias deveria ser falha técnica, veio %v", err)
	}
}

func TestEngineSecurityDataSemLabelFalha(t *testing.T) {
	engineTest(t)
	if _, err := securityDataFromContainer(context.Background(), "teste", dataImage(""), time.Now(), 48, 168); err == nil {
		t.Fatal("sem label deveria falhar")
	}
}

package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Rodam com TG368_ENGINE_TESTS=1:
//
//	TG368_ENGINE_TESTS=1 dagger run go test -run TestEngine ./...
//
// A chave usada é falsa. O teste prova a trava do critério 3: o valor do segredo achado na base
// aborta a publicação, sem aparecer na mensagem.

const fakeNvdKey = "chave-falsa-tg368-0000"

func TestEngineAssertNoSecret(t *testing.T) {
	if os.Getenv("TG368_ENGINE_TESTS") != "1" {
		t.Skip("defina TG368_ENGINE_TESTS=1 para rodar contra o engine do Dagger")
	}
	ctx := context.Background()
	secret := dag.SetSecret("nvd-key-teste", fakeNvdKey)
	limpa := dag.Directory().WithNewFile("odc.mv.db", "base sem segredo").WithNewFile("cache/x", "nada")
	if err := assertNoSecret(ctx, limpa, secret); err != nil {
		t.Fatalf("base limpa não deveria falhar: %v", err)
	}
	suja := limpa.WithNewFile("cache/vazou.properties", "key="+fakeNvdKey+"\n")
	err := assertNoSecret(ctx, suja, secret)
	if err == nil {
		t.Fatal("base com o valor da chave deveria abortar a publicação")
	}
	if strings.Contains(err.Error(), fakeNvdKey) {
		t.Errorf("a mensagem de erro vazou o valor do segredo: %v", err)
	}
}

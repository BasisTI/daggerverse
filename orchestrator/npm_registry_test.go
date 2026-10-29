package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"dagger/orchestrator/internal/dagger"

	"github.com/BasisTI/daggerverse/pipeline/config"
)

// TestNpmOptsLevaCredencial confere o conteúdo das opções: usuário e o mesmo Secret, e não só o
// formato da chamada. Um `RegistryPassword: nil` em npmOpts faria o `npm ci` voltar ao E401.
func TestNpmOptsLevaCredencial(t *testing.T) {
	secret := &dagger.Secret{}
	rt := config.ResolvedTarget{NpmBuildImage: "node:14", NpmRunImage: "nginx:1"}
	opts := npmOpts(rt, registryCredential{user: "jenkins", password: secret})
	if opts.RegistryUser != "jenkins" || opts.RegistryPassword != secret {
		t.Fatalf("npmOpts perdeu a credencial: user=%q password=%p (quero %p)", opts.RegistryUser, opts.RegistryPassword, secret)
	}
	if opts.BuildImage != "node:14" || opts.RunImage != "nginx:1" {
		t.Fatalf("npmOpts perdeu as imagens: %+v", opts)
	}
	if empty := npmOpts(rt, registryCredential{}); empty.RegistryUser != "" || empty.RegistryPassword != nil {
		t.Fatalf("sem credencial, npmOpts devia ir sem ela: %+v", empty)
	}
}

// TestNpmRecebeCredencialDoRegistro garante que a credencial chega a npmOpts por todos os caminhos:
// cada dag.Npm das estratégias, cada função pública que monta a credencial para a análise e cada
// ligação entre elas (qualityTargets, qualityStrategy, checkNpm). O
// conteúdo de npmOpts é do TestNpmOptsLevaCredencial; aqui vale o texto exato de cada chamada,
// para que uma credencial pela metade (`registryCredential{user: registryUser}`) não passe.
func TestNpmRecebeCredencialDoRegistro(t *testing.T) {
	const fromParams = "registryCredential{user: registryUser, password: registryPassword}"
	const fromCheckParams = "registryCredential{host: registryHostOrDefault(registry), user: registryUser, password: registryPassword}"
	read := func(name string) string {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(src)
	}

	strategies := read("strategies.go")
	calls := regexp.MustCompile(`dag\.Npm\(source, ([^\n]*)\)\n`).FindAllStringSubmatch(strategies, -1)
	if len(calls) != strings.Count(strategies, "dag.Npm(") || len(calls) != 2 {
		t.Fatalf("dag.Npm( fora do formato esperado em strategies.go: o teste perdeu o alvo")
	}
	allowed := map[string]bool{
		// publishNpm: a credencial que publica a imagem.
		"npmOpts(rt, " + fromParams + ")": true,
		// checkNpm: opts vem de npmOpts(rt, registry), conferido abaixo.
		"opts": true,
	}
	for _, c := range calls {
		if !allowed[c[1]] {
			t.Errorf("dag.Npm(source, %s) não passa a credencial do registro inteira", c[1])
		}
	}
	if !strings.Contains(strategies, "opts := npmOpts(rt, registry)\n") {
		t.Errorf("checkNpm não monta opts com npmOpts(rt, registry)")
	}
	if n := strings.Count(strategies, "npmOpts(rt, "); n != 2 {
		t.Errorf("npmOpts chamado %d vezes em strategies.go; o teste conhece 2", n)
	}

	// No meio da cadeia, qualityTargets repassa a credencial a qualityStrategy, e esta a checkNpm.
	// Não há como conferir o conteúdo aqui sem engine: a estratégia é uma closure que só chama
	// dag.Npm quando roda. Então vale o texto exato de cada ligação, uma por arquivo.
	if n := strings.Count(strategies, "checkNpm(rt, sonarExtra, registry)"); n != 1 ||
		strings.Count(strategies, "checkNpm(") != 2 {
		t.Errorf("qualityStrategy devia repassar registry a checkNpm, e só por uma chamada")
	}
	targets := read("targets.go")
	if n := strings.Count(targets, "qualityStrategy(rt, sonarExtra, nvdApiKey, registry)"); n != 1 ||
		strings.Count(targets, "qualityStrategy(") != 1 {
		t.Errorf("qualityTargets devia repassar registry a qualityStrategy, e só por uma chamada")
	}
	if !strings.Contains(targets, "nvdApiKey *dagger.Secret, registry registryCredential)") {
		t.Errorf("qualityTargets não recebe a credencial como registry: o teste perdeu o alvo")
	}

	// check-quality e check-quality-from-reports repassam os parâmetros a qualityTargets.
	main := read("main.go")
	if n := strings.Count(main, "qualityTargets(cfg, sonarExtra, nvdApiKey, "+fromCheckParams+")"); n != 2 ||
		strings.Count(main, "qualityTargets(") != 2 {
		t.Errorf("check-quality e check-quality-from-reports deviam repassar %s a qualityTargets", fromCheckParams)
	}
}

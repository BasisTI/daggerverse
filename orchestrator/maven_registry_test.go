package main

import (
	"os"
	"strings"
	"testing"

	"dagger/orchestrator/internal/dagger"

	"github.com/BasisTI/daggerverse/pipeline/config"
)

// TestMavenOptsLevaCredencial confere o conteúdo das opções: host, usuário e o mesmo Secret. Um
// `RegistryPassword: nil` em mavenOpts devolveria o 401 do Testcontainers no dind.
func TestMavenOptsLevaCredencial(t *testing.T) {
	secret := &dagger.Secret{}
	rt := config.ResolvedTarget{MavenImage: "maven:3", UseDocker: true}
	opts := mavenOpts(rt, nil, registryCredential{host: "basis-registry.basis.com.br", user: "jenkins", password: secret})
	if opts.RegistryHost != "basis-registry.basis.com.br" || opts.RegistryUser != "jenkins" || opts.RegistryPassword != secret {
		t.Fatalf("mavenOpts perdeu a credencial: %+v", opts)
	}
	if !opts.UseDocker || opts.BuildImage != "maven:3" {
		t.Fatalf("mavenOpts perdeu as opções do target: %+v", opts)
	}
	if empty := mavenOpts(rt, nil, registryCredential{}); empty.RegistryHost != "" || empty.RegistryUser != "" || empty.RegistryPassword != nil {
		t.Fatalf("sem credencial, mavenOpts devia ir sem ela: %+v", empty)
	}
}

func TestRegistryHostOrDefault(t *testing.T) {
	if got := registryHostOrDefault(""); got != DefaultRegistryHost {
		t.Errorf("vazio = %q, quero %q", got, DefaultRegistryHost)
	}
	if got := registryHostOrDefault("  "); got != DefaultRegistryHost {
		t.Errorf("em branco = %q, quero %q", got, DefaultRegistryHost)
	}
	if got := registryHostOrDefault("nexus.example.com"); got != "nexus.example.com" {
		t.Errorf("informado = %q, quero nexus.example.com", got)
	}
}

// TestMavenRecebeCredencialDoRegistro garante a ligação de cada caminho que roda testes: o publish
// (credencial e host que publicam a imagem) e o check completo (a recebida por qualityTargets). As
// estratégias são closures que só chamam dag.Maven com engine, então vale o texto exato; os caminhos
// que não rodam testes (análise sobre relatórios, inventário, varredura) seguem sem credencial.
func TestMavenRecebeCredencialDoRegistro(t *testing.T) {
	read := func(name string) string {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(src)
	}
	strategies := read("strategies.go")
	want := map[string]int{
		"dag.Maven(mavenOpts(rt, nvdApiKey, registryCredential{host: registry, user: registryUser, password: registryPassword}))": 1, // publishMaven
		"dag.Maven(mavenOpts(rt, nvdApiKey, registry))":             1, // checkMaven
		"dag.Maven(mavenOpts(rt, nvdApiKey, registryCredential{}))": 1, // checkMavenFromReports
	}
	for call, n := range want {
		if got := strings.Count(strategies, call); got != n {
			t.Errorf("%q aparece %d vezes em strategies.go, quero %d", call, got, n)
		}
	}
	if got := strings.Count(strategies, "dag.Maven("); got != 4 {
		t.Errorf("dag.Maven( aparece %d vezes em strategies.go; o teste conhece 4 (as 3 acima e a varredura de segurança, que recebe a credencial como o check completo)", got)
	}
	if !strings.Contains(strategies, "checkMaven(rt, sonarExtra, nvdApiKey, registry), nil") {
		t.Error("qualityStrategy não repassa a credencial a checkMaven")
	}
	if !strings.Contains(strategies, "opts := mavenOpts(rt, nvdApiKey, registry)\n\t\topts.ExtraOptions") {
		t.Error("securityMaven não repassa a credencial do registro ao módulo maven")
	}
	if !strings.Contains(read("targets.go"), "scan: securityMaven(rt, nvdApiKey, registry)") {
		t.Error("securityTargets não repassa a credencial a securityMaven")
	}
	if !strings.Contains(read("main.go"), "securityTargets(cfg, nvdApiKey, registryCredential{host: registryHostOrDefault(registry), user: registryUser, password: registryPassword})") {
		t.Error("SecurityCheck não monta a credencial a partir de --registry/--registry-user/--registry-password")
	}
	if !strings.Contains(read("reuse.go"), "mavenOpts(rt, nvdApiKey, registryCredential{})") {
		t.Error("o inventário de reuse.go perdeu a chamada sem credencial")
	}
}

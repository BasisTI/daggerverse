package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDockerAuthConfigJSON(t *testing.T) {
	out, err := dockerAuthConfigJSON("basis-registry.basis.com.br", "jenkins", "s3nha:com:dois-pontos")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("JSON inválido: %v\n%s", err, out)
	}
	// Só o host do registro: nenhuma outra chave, nem a do Docker Hub.
	if len(cfg.Auths) != 1 {
		t.Fatalf("auths devia ter só o host do registro: %s", out)
	}
	entry, ok := cfg.Auths["basis-registry.basis.com.br"]
	if !ok {
		t.Fatalf("falta a entrada do host: %s", out)
	}
	raw, err := base64.StdEncoding.DecodeString(entry.Auth)
	if err != nil || string(raw) != "jenkins:s3nha:com:dois-pontos" {
		t.Fatalf("auth = %q (%v), quero usuário:senha em base64", entry.Auth, err)
	}
}

func TestRegistryAuthHost(t *testing.T) {
	cases := map[string]string{
		"basis-registry.basis.com.br":                 "basis-registry.basis.com.br",
		"https://basis-registry.basis.com.br":         "basis-registry.basis.com.br",
		"https://Basis-Registry.basis.com.br/":        "basis-registry.basis.com.br",
		"basis-registry.basis.com.br./grupo/imagem":   "basis-registry.basis.com.br",
		"  basis-registry.basis.com.br:8443  ":        "basis-registry.basis.com.br:8443",
		"https://user:pw@basis-registry.basis.com.br": "basis-registry.basis.com.br",
	}
	for in, want := range cases {
		got, err := registryAuthHost(in)
		if err != nil || got != want {
			t.Errorf("registryAuthHost(%q) = %q, %v; quero %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "https://", "/so/caminho", "a b.com", "[::1]:5000"} {
		if got, err := registryAuthHost(bad); err == nil {
			t.Errorf("registryAuthHost(%q) = %q; quero erro", bad, got)
		}
	}
}

// TestDockerAuthNaoVazaEmClaro é um guarda de fonte: a credencial só entra no container de build
// como segredo. WithEnvVariable de DOCKER_AUTH_CONFIG, ou o valor num log, a deixaria em claro.
func TestDockerAuthNaoVazaEmClaro(t *testing.T) {
	for _, name := range []string{"maven.go", "docker.go", "main.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), `WithEnvVariable("DOCKER_AUTH_CONFIG"`) {
			t.Errorf("%s: DOCKER_AUTH_CONFIG em WithEnvVariable; use WithSecretVariable", name)
		}
	}
	src, _ := os.ReadFile("maven.go")
	if !strings.Contains(string(src), `WithSecretVariable("DOCKER_AUTH_CONFIG", m.DockerAuthConfig)`) {
		t.Error("maven.go: DOCKER_AUTH_CONFIG deixou de ir por WithSecretVariable")
	}
}

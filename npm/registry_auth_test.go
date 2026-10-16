package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"dagger/npm/internal/dagger"
)

func TestRegistryFromNpmrc(t *testing.T) {
	cases := map[string]struct {
		npmrc string
		want  string
	}{
		"sem arquivo":        {"", ""},
		"linha simples":      {"registry=https://nexus.basis.com.br/repository/npm-registry\n", "https://nexus.basis.com.br/repository/npm-registry"},
		"espaços e aspas":    {"  registry = \"https://r.example/npm/\"  \n", "https://r.example/npm/"},
		"comentários":        {"# registry=https://a/\n; registry=https://b/\nfoo=bar\n", ""},
		"registro de escopo": {"@basis:registry=https://r.example/npm/\n", ""},
		"última vence":       {"registry=https://a.example/\nregistry=https://b.example/\n", "https://b.example/"},
		"crlf":               {"registry=https://r.example/npm/\r\n", "https://r.example/npm/"},
		"chave com prefixo":  {"strict-registry=https://x/\n", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := registryFromNpmrc(c.npmrc); got != c.want {
				t.Fatalf("registryFromNpmrc(%q) = %q, quero %q", c.npmrc, got, c.want)
			}
		})
	}
}

func TestRegistryAuthKeys(t *testing.T) {
	cases := map[string][]string{
		// Sem barra final: o npm resolve '.' e descarta o último segmento; entram as duas formas.
		"https://nexus.basis.com.br/repository/npm-registry": {
			"//nexus.basis.com.br/repository/npm-registry/", "//nexus.basis.com.br/repository/",
		},
		"https://nexus.basis.com.br/repository/npm-registry/": {"//nexus.basis.com.br/repository/npm-registry/"},
		"http://registry:4873":                                {"//registry:4873/"},
		"https://registry.example/":                           {"//registry.example/"},
	}
	for registry, want := range cases {
		got, err := registryAuthKeys(registry)
		if err != nil {
			t.Fatalf("registryAuthKeys(%q): %v", registry, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("registryAuthKeys(%q) = %v, quero %v", registry, got, want)
		}
	}
	for _, bad := range []string{"nexus.basis.com.br/repository", "file:///tmp/x", "${NPM_REGISTRY}"} {
		if _, err := registryAuthKeys(bad); err == nil {
			t.Fatalf("registryAuthKeys(%q) devia falhar", bad)
		}
	}
}

// O registro público não recebe a credencial do registry da Basis, nem declarado explicitamente.
func TestRegistryAuthKeysRegistroPublico(t *testing.T) {
	for _, public := range []string{
		"https://registry.npmjs.org/", "https://registry.npmjs.org", "http://registry.npmjs.org/",
		"https://REGISTRY.NPMJS.ORG/", "https://registry.npmjs.org:443/", "https://registry.yarnpkg.com/",
		// O ponto final DNS é o mesmo host.
		"https://registry.npmjs.org./", "https://registry.yarnpkg.com./", "https://Registry.NPMJS.org.:443/",
		"https://registry.npmjs.org../",
	} {
		if _, err := registryAuthKeys(public); !errors.Is(err, errPublicRegistry) {
			t.Fatalf("registryAuthKeys(%q): quero errPublicRegistry, veio %v", public, err)
		}
	}
	// Só o host exato: um Nexus com "npmjs" no caminho continua recebendo.
	if _, err := registryAuthKeys("https://nexus.example/repository/registry.npmjs.org/"); err != nil {
		t.Fatalf("registro privado com npmjs no caminho: %v", err)
	}
}

// O userconfig só tem o _auth por chave, por referência à variável: sem always-auth, que mandaria a
// credencial a tarballs de outros hosts no npm 6, e sem o valor.
func TestRegistryAuthUserconfig(t *testing.T) {
	got, keys, err := registryAuthUserconfigFor("https://nexus.basis.com.br/repository/npm-registry")
	if err != nil {
		t.Fatal(err)
	}
	want := "//nexus.basis.com.br/repository/npm-registry/:_auth=${NPM_REGISTRY_AUTH}\n" +
		"//nexus.basis.com.br/repository/:_auth=${NPM_REGISTRY_AUTH}\n"
	if got != want {
		t.Fatalf("userconfig = %q, quero %q", got, want)
	}
	if len(keys) != 2 || strings.Contains(got, "always-auth") {
		t.Fatalf("chaves %v, userconfig %q", keys, got)
	}
	if _, _, err := registryAuthUserconfigFor("https://registry.npmjs.org/"); !errors.Is(err, errPublicRegistry) {
		t.Fatalf("registro público: quero errPublicRegistry, veio %v", err)
	}
}

func TestRegistryAuthSemUsuario(t *testing.T) {
	n := New(nil, "", "", false, "", "", &dagger.Secret{})
	if _, err := n.registryAuth(context.Background()); err == nil {
		t.Fatal("senha sem usuário devia falhar, e não seguir sem credencial em silêncio")
	}
	if auth, err := New(nil, "", "", false, "", "", nil).registryAuth(context.Background()); auth != nil || err != nil {
		t.Fatalf("sem credencial: quero (nil, nil), veio (%v, %v)", auth, err)
	}
}

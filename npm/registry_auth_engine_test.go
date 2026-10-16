package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger/npm/internal/dagger"
)

// Os testes abaixo sobem um registro npm com autenticação (Verdaccio com htpasswd) como serviço do
// Dagger e rodam o FullBuild de verdade contra ele, por isso só rodam com TG331_ENGINE_TESTS=1:
//
//	TG331_ENGINE_TESTS=1 dagger run go test -run TestEngine -v ./...
//
// Com TG331_EXPORT_DIR, as imagens montadas são exportadas como tarball para inspeção externa.
//
// A credencial é fictícia e só existe aqui. Ela entra no engine sempre como segredo (inclusive o
// htpasswd e a lista de padrões do grep), para que a saída do `dagger run` possa ser varrida atrás
// dela depois.

const (
	fakeRegistryUser     = "tg331-ficticio"
	fakeRegistryPassword = "Fict1cia-TG331-nao-e-real"
	verdaccioImage       = "verdaccio/verdaccio:6.1.6"
	inspectImage         = "alpine:3.22"
)

func fakeRegistryAuth() string {
	return base64.StdEncoding.EncodeToString([]byte(fakeRegistryUser + ":" + fakeRegistryPassword))
}

func registryEngineTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TG331_ENGINE_TESTS") != "1" {
		t.Skip("defina TG331_ENGINE_TESTS=1 para rodar contra o engine do Dagger")
	}
}

// fakeRegistry sobe um Verdaccio que recusa leitura e publicação anônimas, como o Nexus fechado.
func fakeRegistry() *dagger.Service {
	sum := sha1.Sum([]byte(fakeRegistryPassword))
	htpasswd := fmt.Sprintf("%s:{SHA}%s\n", fakeRegistryUser, base64.StdEncoding.EncodeToString(sum[:]))
	config := `storage: /verdaccio/storage
auth:
  htpasswd:
    file: /verdaccio/secret/htpasswd
    max_users: -1
uplinks: {}
packages:
  '**':
    access: $authenticated
    publish: $authenticated
log: { type: stdout, format: pretty, level: http }
`
	return dag.Container().From(verdaccioImage).
		WithNewFile("/verdaccio/conf/config.yaml", config).
		WithMountedSecret("/verdaccio/secret/htpasswd", dag.SetSecret("tg331-htpasswd", htpasswd),
			dagger.ContainerWithMountedSecretOpts{Mode: 0o444}).
		WithExposedPort(4873).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
}

// withRegistry liga o registro a um container por dois nomes: `registry`, o do .npmrc, e `element`,
// outro nome do mesmo servidor -- como o lockfile v1 do portal-liven, que guarda os tarballs em
// http://element.basis.com.br enquanto o .npmrc aponta para https://nexus.basis.com.br.
func withRegistry(c *dagger.Container, svc *dagger.Service) *dagger.Container {
	return c.WithServiceBinding("registry", svc).WithServiceBinding("element", svc)
}

// authNpmrc é o .npmrc dos passos de preparação (publicar o pacote, gerar o lockfile).
const authNpmrc = "registry=http://registry:4873/\n//registry:4873/:_auth=${NPM_AUTH}\n//element:4873/:_auth=${NPM_AUTH}\n"

// publishFixtureDependency publica no registro um pacote de nome único, para que nenhum cache de
// execução anterior sirva o tarball sem passar pela autenticação.
func publishFixtureDependency(ctx context.Context, svc *dagger.Service, auth *dagger.Secret, name string) error {
	pkg := fmt.Sprintf(`{"name":%q,"version":"1.0.0","main":"index.js"}`, name)
	_, err := withRegistry(dag.Container().From("node:22.16.0-alpine3.22"), svc).
		WithNewFile("/pkg/package.json", pkg).
		WithNewFile("/pkg/index.js", "module.exports = 'dependencia do registro privado';\n").
		WithNewFile("/pkg/.npmrc", authNpmrc).
		WithWorkdir("/pkg").
		WithSecretVariable("NPM_AUTH", auth).
		WithExec([]string{"npm", "publish"}).
		Sync(ctx)
	return err
}

// fixtureProject é o projeto npm mínimo: depende do pacote privado e o build copia o pacote para dist/.
// O lockfile é gerado com a imagem do cenário (npm 6 grava lockfile v1) e, se aliasHost vier, as URLs
// dos tarballs passam a apontar para o outro nome do registro.
func fixtureProject(ctx context.Context, svc *dagger.Service, auth *dagger.Secret, image, dep string, aliasHost bool) (*dagger.Directory, error) {
	pkg := fmt.Sprintf(`{
  "name": "tg331-fixture",
  "version": "0.0.1",
  "private": true,
  "dependencies": {%q: "1.0.0"},
  "scripts": {"build": "mkdir -p dist && cp node_modules/%s/index.js dist/dep.js && echo ok > dist/index.html"}
}
`, dep, dep)
	src := dag.Directory().
		WithNewFile("package.json", pkg).
		WithNewFile(".npmrc", "registry=http://registry:4873/\n")
	lock, err := withRegistry(dag.Container().From(image), svc).
		WithDirectory("/fx", src).
		WithNewFile("/fx/.npmrc", authNpmrc).
		WithWorkdir("/fx").
		WithSecretVariable("NPM_AUTH", auth).
		WithExec([]string{"npm", "install", "--package-lock-only"}).
		File("/fx/package-lock.json").Contents(ctx)
	if err != nil {
		return nil, err
	}
	if aliasHost {
		if !strings.Contains(lock, "http://registry:4873/") {
			return nil, fmt.Errorf("lockfile sem URL do registro para trocar pelo alias:\n%s", lock)
		}
		lock = strings.ReplaceAll(lock, "http://registry:4873/", "http://element:4873/")
	}
	return src.WithNewFile("package-lock.json", lock), nil
}

// leaks devolve os arquivos regulares sob root que contêm algum padrão da credencial: o usuário, a
// senha, `usuário:senha` e o `_auth` em base64. Os padrões entram como segredo, para não irem para o
// log. Falha o teste se a varredura não encontra arquivo nenhum -- uma varredura vazia passaria por
// "nada encontrado".
func leaks(ctx context.Context, t *testing.T, what string, c *dagger.Container, root string) []string {
	t.Helper()
	patterns := strings.Join([]string{fakeRegistryUser, fakeRegistryPassword,
		fakeRegistryUser + ":" + fakeRegistryPassword, fakeRegistryAuth()}, "\n") + "\n"
	script := `n=$(find "$0" -type f | wc -l); echo "files=$n"; find "$0" -type f -exec grep -lF -f /tmp/leak-patterns {} + || true`
	out, err := c.
		WithMountedSecret("/tmp/leak-patterns", dag.SetSecret("tg331-leak-patterns", patterns)).
		WithExec([]string{"sh", "-c", script, root}).
		Stdout(ctx)
	if err != nil {
		t.Fatalf("%s: varredura falhou: %v", what, err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "files=") || lines[0] == "files=0" {
		t.Fatalf("%s: varredura sem arquivos: %q", what, out)
	}
	t.Logf("%s: %s varridos", what, lines[0])
	return lines[1:]
}

func noLeak(ctx context.Context, t *testing.T, what string, c *dagger.Container, root string) {
	t.Helper()
	if found := leaks(ctx, t, what, c, root); len(found) > 0 {
		t.Fatalf("%s: credencial encontrada em %v", what, found)
	}
}

func TestEngineRegistryAuth(t *testing.T) {
	registryEngineTest(t)
	ctx := context.Background()
	svc := fakeRegistry()
	auth := dag.SetSecret("tg331-npm-auth", fakeRegistryAuth())
	password := dag.SetSecret("tg331-npm-password", fakeRegistryPassword)

	// Controle da varredura: um arquivo com o _auth tem de ser encontrado.
	planted := dag.Container().From(inspectImage).
		WithMountedSecret("/tmp/auth", auth).
		WithExec([]string{"sh", "-c", "mkdir -p /p && cp /tmp/auth /p/npmrc && echo limpo > /p/outro"})
	if found := leaks(ctx, t, "controle", planted, "/p"); len(found) != 1 || found[0] != "/p/npmrc" {
		t.Fatalf("a varredura devia achar o _auth plantado em /p/npmrc; achou %v", found)
	}

	scenarios := []struct {
		name, image string
		aliasHost   bool
	}{
		// O default do módulo: npm 10, lockfile v3, tarballs no host do .npmrc.
		{"node22", "node:22.16.0-alpine3.22", false},
		// O portal-liven: npm 6, lockfile v1, tarballs em outro nome do registro.
		{"node14-lockv1-alias", "node:14.19.2-alpine3.15", true},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			dep := fmt.Sprintf("tg331-dep-%d", time.Now().UnixNano())
			if err := publishFixtureDependency(ctx, svc, auth, dep); err != nil {
				t.Fatalf("publicar a dependência no registro: %v", err)
			}
			src, err := fixtureProject(ctx, svc, auth, sc.image, dep, sc.aliasHost)
			if err != nil {
				t.Fatalf("montar o projeto: %v", err)
			}
			newNpm := func(user string, password *dagger.Secret) *Npm {
				n := New(src, sc.image, "nginx:1.27.3-alpine-slim", true, "", user, password)
				n.NodeContainer = withRegistry(n.NewContainer(), svc)
				return n
			}

			// Sem credencial: o registro recusa, como o Nexus no job 24760.
			_, err = newNpm("", nil).FullBuild(ctx, "", "", nil, nil, nil)
			var execErr *dagger.ExecError
			if !errors.As(err, &execErr) || !strings.Contains(execErr.Stderr, "E401") {
				t.Fatalf("sem credencial o npm ci devia falhar com E401; erro: %v", err)
			}

			// Com credencial: instala, builda e monta a imagem.
			// A versão vai no package.json antes, e não pelo FullBuild: com versão, ele recria o
			// container e perderia a ligação com o registro que o teste injeta.
			n := newNpm(fakeRegistryUser, password)
			if n.Source, err = n.withPackageJsonVersion(ctx, "2026.10.16.1"); err != nil {
				t.Fatalf("versão no package.json: %v", err)
			}
			n.NodeContainer = withRegistry(n.NewContainer(), svc)
			result, err := n.FullBuild(ctx, "abc123", "", nil, nil, nil)
			if err != nil {
				t.Fatalf("com credencial o build devia passar: %v", err)
			}
			image := n.runtimeImage(result, "2026.10.16.1", "abc123")
			depJS, err := image.File("/usr/share/nginx/html/dep.js").Contents(ctx)
			if err != nil || !strings.Contains(depJS, "registro privado") {
				t.Fatalf("a imagem devia trazer o dist com a dependência: %q, %v", depJS, err)
			}

			// Nada da credencial sobra: nem no container de build, nem na imagem, nem no cache do npm.
			for _, c := range []struct {
				what string
				c    *dagger.Container
			}{{"container de build", result.Container}, {"imagem", image}} {
				for _, env := range []string{registryAuthEnv, "NPM_CONFIG_USERCONFIG"} {
					if v, _ := c.c.EnvVariable(ctx, env); v != "" {
						t.Fatalf("%s: variável %s ficou definida", c.what, env)
					}
				}
			}
			if _, err := result.Container.File(registryAuthUserconfig).Contents(ctx); err == nil {
				t.Fatalf("container de build: %s ficou no container", registryAuthUserconfig)
			}
			inspect := dag.Container().From(inspectImage)
			noLeak(ctx, t, "container de build", inspect.WithMountedDirectory("/r", result.Container.Rootfs()), "/r")
			noLeak(ctx, t, "imagem", inspect.WithMountedDirectory("/r", image.Rootfs()), "/r")
			noLeak(ctx, t, "cache do npm", inspect.WithMountedCache("/c", dag.CacheVolume(DefaultNpmCacheName)), "/c")

			if dir := os.Getenv("TG331_EXPORT_DIR"); dir != "" {
				path := filepath.Join(dir, "imagem-"+sc.name+".tar")
				if _, err := image.Export(ctx, path); err != nil {
					t.Fatalf("exportar a imagem: %v", err)
				}
			}
		})
	}
}

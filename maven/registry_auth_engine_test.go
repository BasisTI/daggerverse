package main

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"dagger/maven/internal/dagger"
)

// Os testes abaixo rodam um build Maven de verdade, com use-docker, e conferem o que o Testcontainers
// do build enxerga da credencial do registro, por isso só rodam com TG356_ENGINE_TESTS=1:
//
//	TG356_ENGINE_TESTS=1 dagger run go test -run TestRegistryAuthEngine -v ./...
//
// O teste do projeto pergunta ao RegistryAuthLocator do próprio Testcontainers -- o código que
// escolhe a credencial de cada pull -- qual credencial ele usaria para a imagem de cada host. Não
// puxa imagem: o que se prova é que a credencial chega ao Testcontainers, vale para o host do
// registro e para nenhum outro. A rede entre o dind e um registro com login fica de fora.
//
// A credencial é fictícia e só existe aqui. Entra no engine como segredo, para a saída do
// `dagger run` poder ser varrida atrás dela.

const (
	tcRegistryHost     = "registro.tg356.test"
	tcRegistryUser     = "tg356-ficticio"
	tcRegistryPassword = "Fict1cia-TG356-nao-e-real"
	testcontainersDep  = `<dependency><groupId>org.testcontainers</groupId><artifactId>testcontainers</artifactId><version>1.21.4</version><scope>test</scope></dependency>`
	inspectImage       = "alpine:3.22"
)

func registryAuthEngineTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TG356_ENGINE_TESTS") != "1" {
		t.Skip("defina TG356_ENGINE_TESTS=1 para rodar contra o engine do Dagger")
	}
}

// authSource é um projeto cujo único teste pergunta ao Testcontainers a credencial de cada imagem.
// Com expectCredential falso, o teste exige que ele não ache nenhuma, nem no host do registro.
func authSource(expectCredential bool) *dagger.Directory {
	pom := `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>test</groupId><artifactId>auth</artifactId><version>1.0</version><properties><maven.compiler.release>21</maven.compiler.release></properties><dependencies>` + junitDepOnly + testcontainersDep + `</dependencies></project>`
	check := `
		AuthConfig mine = locator.lookupAuthConfig(DockerImageName.parse("` + tcRegistryHost + `/grupo/imagem:1"), new AuthConfig());
		assertNull(mine.getUsername());
		assertNull(mine.getPassword());
		assertNull(System.getenv("DOCKER_AUTH_CONFIG"));`
	if expectCredential {
		check = `
		AuthConfig mine = locator.lookupAuthConfig(DockerImageName.parse("` + tcRegistryHost + `/grupo/imagem:1"), new AuthConfig());
		assertEquals("` + tcRegistryUser + `", mine.getUsername());
		assertEquals("` + tcRegistryPassword + `", mine.getPassword());`
	}
	test := `package demo;
import org.junit.Test;
import static org.junit.Assert.*;
import com.github.dockerjava.api.model.AuthConfig;
import org.testcontainers.utility.DockerImageName;
import org.testcontainers.utility.RegistryAuthLocator;
public class AuthTest {
	@Test public void credencialPorHost() {
		RegistryAuthLocator locator = RegistryAuthLocator.instance();` + check + `
		// Nenhum outro host recebe a credencial: nem o Docker Hub, nem um registro de terceiro.
		for (String other : new String[] {"minio/minio:1", "quay.io/minio/minio:1", "outro-` + tcRegistryHost + `/grupo/imagem:1", "` + tcRegistryHost + `.evil.test/grupo/imagem:1"}) {
			AuthConfig cfg = locator.lookupAuthConfig(DockerImageName.parse(other), new AuthConfig());
			assertNull(other, cfg.getUsername());
			assertNull(other, cfg.getPassword());
		}
	}
}`
	return dag.Directory().WithNewFile("pom.xml", pom).
		WithNewFile("src/test/java/demo/AuthTest.java", test)
}

const junitDepOnly = `<dependency><groupId>junit</groupId><artifactId>junit</artifactId><version>4.13.2</version><scope>test</scope></dependency>`

// cleanCi não liga o -Dmaven.test.failure.ignore=true das opções de CI: sem isso, um teste vermelho
// não derrubaria o build e o teste do engine passaria por engano.
func authMaven(t *testing.T, registryHost, user string, password *dagger.Secret) *Maven {
	t.Helper()
	return mustNew(New(context.Background(), mavenFixtureImage, false, true, false, []string{"--batch-mode"}, false, false, true, "", nil,
		registryHost, user, password))
}

func fakePassword() *dagger.Secret { return dag.SetSecret("tg356-password", tcRegistryPassword) }

func TestRegistryAuthEngineComCredencial(t *testing.T) {
	registryAuthEngineTest(t)
	ctx := context.Background()
	m := authMaven(t, "https://"+strings.ToUpper(tcRegistryHost)+"/", tcRegistryUser, fakePassword())
	if m.DockerAuthConfig == nil {
		t.Fatal("com credencial e use-docker, DockerAuthConfig devia existir")
	}
	// Se o teste do projeto fica vermelho, o build falha (sem failure.ignore) e FullBuild devolve erro.
	built, err := m.FullBuild(ctx, authSource(true), "auth", "", "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("o Testcontainers do build não achou a credencial do registro: %v", err)
	}
	assertCredentialNowhere(t, m, built)
}

func TestRegistryAuthEngineSemCredencial(t *testing.T) {
	registryAuthEngineTest(t)
	ctx := context.Background()
	m := authMaven(t, "", "", nil)
	if m.DockerAuthConfig != nil {
		t.Fatal("sem credencial, DockerAuthConfig devia ser nil")
	}
	if _, err := m.FullBuild(ctx, authSource(false), "auth", "", "", nil, nil, nil, ""); err != nil {
		t.Fatalf("sem credencial o build devia seguir como antes, sem DOCKER_AUTH_CONFIG: %v", err)
	}
}

// Credencial sem use-docker não tem para quem ir: o módulo não monta nada.
func TestRegistryAuthEngineSemUseDocker(t *testing.T) {
	registryAuthEngineTest(t)
	m := mustNew(New(context.Background(), mavenFixtureImage, false, true, false, nil, false, false, false, "", nil,
		tcRegistryHost, tcRegistryUser, fakePassword()))
	if m.DockerAuthConfig != nil {
		t.Fatal("sem use-docker, a credencial não devia virar DOCKER_AUTH_CONFIG")
	}
}

// Senha sem usuário ou sem host é erro de uso: seguir sem autenticar só mostraria o 401 no teste.
func TestRegistryAuthEngineEntradaIncompleta(t *testing.T) {
	registryAuthEngineTest(t)
	ctx := context.Background()
	if _, err := New(ctx, mavenFixtureImage, false, true, true, nil, false, false, true, "", nil, tcRegistryHost, "", fakePassword()); err == nil {
		t.Error("senha sem usuário devia falhar")
	}
	if _, err := New(ctx, mavenFixtureImage, false, true, true, nil, false, false, true, "", nil, "", tcRegistryUser, fakePassword()); err == nil {
		t.Error("senha sem host devia falhar")
	}
}

// assertCredentialNowhere procura a senha fictícia, o auth em base64 e o JSON no que o build deixa: a
// árvore do módulo, a saída dos estágios e o ambiente declarado do container. O segredo só existe
// como variável de segredo da execução.
func assertCredentialNowhere(t *testing.T, m *Maven, built *ModuleBuildResult) {
	t.Helper()
	ctx := context.Background()
	auth := base64.StdEncoding.EncodeToString([]byte(tcRegistryUser + ":" + tcRegistryPassword))
	needles := []string{tcRegistryPassword, auth}

	for _, out := range append(append([]string{}, built.Stdout...), built.Stderr...) {
		for _, n := range needles {
			if strings.Contains(out, n) {
				t.Errorf("a saída do build traz a credencial")
			}
		}
	}
	envs, err := m.Container().EnvVariables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range envs {
		name, _ := env.Name(ctx)
		value, _ := env.Value(ctx)
		if name == "DOCKER_AUTH_CONFIG" {
			t.Errorf("DOCKER_AUTH_CONFIG está no ambiente declarado do container")
		}
		for _, n := range needles {
			if strings.Contains(value, n) {
				t.Errorf("a variável %s traz a credencial", name)
			}
		}
	}
	// Procura na árvore inteira do módulo, menos o AuthTest (fonte e classe), que traz o valor
	// esperado como literal. O pattern é segredo para não aparecer no log do grep.
	pattern := dag.SetSecret("tg356-needles", strings.Join(needles, "\n"))
	out, err := dag.Container().From(inspectImage).
		WithDirectory("/tree", built.Tree).
		WithMountedSecret("/needles", pattern).
		WithExec([]string{"sh", "-c", "grep -rlF --exclude='AuthTest*' -f /needles /tree || true"}).
		Stdout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("a credencial foi parar na árvore do módulo: %s", out)
	}
}

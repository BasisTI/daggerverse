package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"dagger/maven/internal/dagger"
)

const (
	// DindImage is the Docker-in-Docker image backing the daemon offered to builds.
	DindImage = "docker:29-dind"
	// DockerPort is the plain TCP port the daemon listens on. TLS is off on purpose: the daemon is
	// only reachable over Dagger's private service network, from the build container that asked
	// for it, and it is discarded when the build ends.
	DockerPort = 2375
	// DockerHostname is the alias the build container resolves the daemon by.
	DockerHostname = "docker"
	// DockerStorageCacheName identifies the volume mounted at the daemon's /var/lib/docker.
	DockerStorageCacheName = "maven-dind-storage"
)

// dockerService returns a Docker daemon for the build to talk to.
//
// Testcontainers needs a real daemon and the Maven image has none, so `mvn verify` fails every
// suite with "Could not find a valid Docker environment" — and, because the CI options include
// -Dmaven.test.failure.ignore=true, it fails without failing the build. Binding a daemon here
// fixes that for every project at once, and without touching test code: a suite written against
// Testcontainers runs unchanged.
func (m *Maven) dockerService() *dagger.Service {
	return dag.Container().
		From(DindImage).
		// Dagger provides pid 1, so tini warns on every run unless told it is not the reaper.
		WithEnvVariable("TINI_SUBREAPER", "true").
		// The daemon's storage has to live on a real volume. Left on the container filesystem it
		// lands on Dagger's own overlayfs, and every pull dies extracting the first whiteout
		// entry -- "failed to convert whiteout file ...: operation not permitted" -- because
		// overlayfs cannot create whiteouts on top of overlayfs.
		//
		// Private sharing, not Shared: two daemons writing the same storage would corrupt it.
		// Not Locked either, which would keep them from corrupting each other by making one wait
		// for the whole of the other's build. The cost is that images are pulled fresh per run.
		WithMountedCache("/var/lib/docker", dag.CacheVolume(DockerStorageCacheName),
			dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModePrivate}).
		WithExposedPort(DockerPort).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"dockerd",
				"--tls=false",
				fmt.Sprintf("--host=tcp://0.0.0.0:%d", DockerPort),
			},
			InsecureRootCapabilities: true,
			UseEntrypoint:            true,
		})
}

// withDocker points a build container at the daemon returned by dockerService.
//
// The binding is attached to the base container rather than to a single stage, so one daemon
// serves the whole build instead of being started and thrown away per stage.
func (m *Maven) withDocker(container *dagger.Container) *dagger.Container {
	return container.
		WithServiceBinding(DockerHostname, m.dockerService()).
		WithEnvVariable("DOCKER_HOST", fmt.Sprintf("tcp://%s:%d", DockerHostname, DockerPort)).
		// Ryuk is the Testcontainers reaper. It has nothing to reap here — the daemon dies with
		// the service — and reaching it back through the service binding is unreliable, so every
		// suite would pay its startup timeout for no gain.
		WithEnvVariable("TESTCONTAINERS_RYUK_DISABLED", "true")
}

// dockerAuthConfig monta o DOCKER_AUTH_CONFIG que o Testcontainers lê: o JSON de ~/.docker/config.json
// com uma única entrada, a do host do registro. Imagem de qualquer outro host é puxada sem
// credencial, então ela não sai para o Docker Hub nem para um registro de terceiro.
//
// O valor é registrado como segredo novo (a senha e o `auth` derivado dela ficam mascarados na
// saída) e só chega ao container de build por WithSecretVariable. Devolve erro se faltar host ou
// usuário, em vez de seguir sem autenticar e deixar o 401 aparecer só no teste.
func dockerAuthConfig(ctx context.Context, host, user string, password *dagger.Secret) (*dagger.Secret, error) {
	host, err := registryAuthHost(host)
	if err != nil {
		return nil, fmt.Errorf("credencial do registro para o Testcontainers: %w", err)
	}
	if user == "" {
		return nil, fmt.Errorf("credencial do registro para o Testcontainers: senha informada sem usuário")
	}
	plain, err := password.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("credencial do registro para o Testcontainers: ler a senha: %w", err)
	}
	contents, err := dockerAuthConfigJSON(host, user, plain)
	if err != nil {
		return nil, err
	}
	fmt.Printf("🔐 Credencial do registro (usuário %s) entregue ao Testcontainers só para %s\n", user, host)
	return dag.SetSecret("maven-docker-auth-config", contents), nil
}

// dockerAuthConfigJSON é o JSON de DOCKER_AUTH_CONFIG para um host: {"auths":{"<host>":{"auth":"<base64 usuário:senha>"}}}.
func dockerAuthConfigJSON(host, user, password string) (string, error) {
	out, err := json.Marshal(map[string]any{
		"auths": map[string]any{
			host: map[string]string{
				"auth": base64.StdEncoding.EncodeToString([]byte(user + ":" + password)),
			},
		},
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// registryAuthHost reduz o que o chamador informou ao host (com porta, se houver) que serve de chave
// em `auths`: sem esquema, sem caminho, em minúsculas e sem o ponto final do DNS. É a chave contra
// a qual o Testcontainers compara o host da imagem; o que não for um host de registro é recusado,
// para que um valor torto não vire uma credencial que nunca casa.
func registryAuthHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	name, port := host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		name, port = host[:i], host[i:]
	}
	name = strings.TrimSuffix(name, ".")
	if name == "" || strings.ContainsAny(name, " \t:[]") {
		return "", fmt.Errorf("host do registro inválido: %q", raw)
	}
	return name + port, nil
}

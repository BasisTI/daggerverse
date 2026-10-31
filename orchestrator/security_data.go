package main

import (
	"context"
	"dagger/orchestrator/internal/dagger"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultSecurityDataImage é a imagem com a base NVD do Dependency-Check, publicada pelo
	// projeto basis/iac/nvd-data. A tag é a versão major do ODC: o esquema do H2 muda entre majors.
	DefaultSecurityDataImage = "basis-registry.basis.com.br/basis/iac/nvd-data/odc-data:13"
	// SecurityDataCreatedLabel é o label OCI com o instante da atualização. Espelha
	// orchestrator-utils.SecurityDataLabel, que o lê.
	SecurityDataCreatedLabel = "org.opencontainers.image.created"
	// securityDataDir é onde a base mora dentro da imagem. Espelha orchestrator-utils.SecurityDataDir.
	securityDataDir = "/odc-data"
	// DefaultOdcVersion é a versão do dependency-check-maven que o aquecimento roda.
	DefaultOdcVersion = "13.0.0"
)

// securityDataPlan decide de onde vem a base do scan. Com a credencial completa do registro, vem da
// imagem; sem ela, o scan cai no modo antigo (o plugin baixa a NVD pela API) e o log diz isso.
// O aviso é devolvido, e não impresso aqui, para o teste conferir a mensagem.
func securityDataPlan(registry registryCredential) (useImage bool, notice string) {
	if registry.user != "" && registry.password != nil {
		return true, ""
	}
	return false, "⚠️  Sem credencial do registro (--registry-user/--registry-password): a base NVD não pode ser puxada " +
		"da imagem de dados, e o Dependency-Check vai atualizá-la pela API da NVD. Isso pode levar horas e cai com 503; " +
		"passe a credencial do registro para usar a base pronta."
}

// securityDataDirectory puxa a base da imagem e força o pull agora, para uma imagem ausente, um 401
// ou uma base velha demais derrubarem o job com a mensagem certa -- e não como erro de varredura
// do primeiro target.
func securityDataDirectory(ctx context.Context, image string, registry registryCredential) (*dagger.Directory, error) {
	dir := dag.OrchestratorUtils().SecurityData(image, registry.host, registry.user, registry.password)
	if _, err := dir.Sync(ctx); err != nil {
		return nil, fmt.Errorf("base NVD %s: %w", image, err)
	}
	return dir, nil
}

// isImageMissing diz se o erro do pull é "a imagem não existe" (bootstrap) e não um problema de
// credencial ou de rede, que tem de falhar o job em vez de recomeçar do zero em silêncio.
func isImageMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "401") || strings.Contains(msg, "denied") {
		return false
	}
	return strings.Contains(msg, "manifest_unknown") || strings.Contains(msg, "not found") || strings.Contains(msg, "404")
}

// UpdateSecurityData atualiza a base NVD do Dependency-Check e a publica como imagem OCI, com o
// instante da atualização no label org.opencontainers.image.created. Roda no projeto
// basis/iac/nvd-data, agendado; os scans de segurança dos projetos só leem essa imagem.
//
// Puxa a publicação anterior para a atualização ser incremental (minutos). Imagem inexistente é
// o bootstrap e baixa a NVD inteira (horas); qualquer outro erro do pull -- 401, rede -- falha o
// job. A chave da NVD só existe neste job, como Secret. Antes de publicar, a base é varrida atrás
// do valor da chave e a publicação aborta se ele aparecer.
//
// Devolve a referência publicada (com digest).
//
// +cache="never"
func (o *Orchestrator) UpdateSecurityData(
	ctx context.Context,
	// Chave da API da NVD.
	nvdApiKey *dagger.Secret,
	// Usuário do registro, com permissão de escrita no repositório da imagem.
	registryUser string,
	// Senha do registro.
	registryPassword *dagger.Secret,
	// Imagem a publicar.
	// +default="basis-registry.basis.com.br/basis/iac/nvd-data/odc-data:13"
	image string,
	// Host do registro da credencial. Sem ele, vale DefaultRegistryHost.
	// +optional
	registry string,
	// Versão do dependency-check-maven.
	// +default="13.0.0"
	odcVersion string,
) (string, error) {
	host := registryHostOrDefault(registry)
	prev := dag.Container().WithRegistryAuth(host, registryUser, registryPassword).From(image)
	var previous *dagger.Directory
	if _, err := prev.Sync(ctx); err != nil {
		if !isImageMissing(err) {
			return "", fmt.Errorf("falha ao puxar a base anterior %s: %w", image, err)
		}
		fmt.Printf("🆕 %s não existe: bootstrap, vai baixar a NVD inteira (horas).\n", image)
	} else {
		fmt.Printf("♻️  Atualização incremental sobre %s.\n", image)
		previous = prev.Directory(securityDataDir)
	}
	data := dag.Maven().UpdateSecurityData(nvdApiKey, dagger.MavenUpdateSecurityDataOpts{
		OdcVersion: odcVersion,
		Previous:   previous,
	})
	if err := assertNoSecret(ctx, data, nvdApiKey); err != nil {
		return "", err
	}
	created := time.Now().UTC().Format(time.RFC3339)
	ref, err := dag.Container().
		WithDirectory(securityDataDir, data).
		WithLabel(SecurityDataCreatedLabel, created).
		WithRegistryAuth(host, registryUser, registryPassword).
		Publish(ctx, image)
	if err != nil {
		return "", fmt.Errorf("publicar %s: %w", image, err)
	}
	fmt.Printf("✅ Base NVD publicada: %s (%s)\n", ref, created)
	return ref, nil
}

// assertNoSecret falha se o valor do segredo aparecer em algum arquivo do diretório. A mensagem
// não traz o valor.
func assertNoSecret(ctx context.Context, dir *dagger.Directory, secret *dagger.Secret) error {
	_, err := dag.Container().From("alpine:3.22").
		WithDirectory("/scan", dir).
		WithSecretVariable("SCAN_SECRET", secret).
		WithExec([]string{"sh", "-c",
			`if grep -rqF -- "$SCAN_SECRET" /scan; then echo "o valor de um segredo apareceu na base; publicação abortada" >&2; exit 1; fi`}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("verificação de segredo na base: %w", err)
	}
	return nil
}

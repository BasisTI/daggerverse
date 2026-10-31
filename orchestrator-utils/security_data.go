package main

import (
	"context"
	"dagger/orchestrator-utils/internal/dagger"
	"fmt"
	"strings"
	"time"
)

const (
	// SecurityDataLabel é o label OCI com o instante da última atualização da base. O job que
	// publica a imagem o escreve; o scan o lê para decidir se a base ainda serve.
	SecurityDataLabel = "org.opencontainers.image.created"
	// SecurityDataDir é onde a base do Dependency-Check mora dentro da imagem de dados.
	SecurityDataDir = "/odc-data"
	// DefaultSecurityDataWarnHours é a idade a partir da qual o scan avisa e segue.
	DefaultSecurityDataWarnHours = 48
	// DefaultSecurityDataFailHours é a idade a partir da qual o scan falha: 7 dias.
	DefaultSecurityDataFailHours = 168
)

// dataFreshness é a classificação da idade da base.
type dataFreshness int

const (
	freshnessOK dataFreshness = iota
	freshnessWarn
	freshnessStale
)

// classifySecurityData lê o label de data e classifica a idade da base. É função pura: o
// instante atual entra como parâmetro. Label ausente ou ilegível é erro, nunca "ok": uma base
// de idade desconhecida não pode passar por recente.
//
// Os limites são exclusivos do lado bom: idade igual ao limite ainda é da faixa de baixo.
func classifySecurityData(created string, now time.Time, warnAfter, failAfter time.Duration) (dataFreshness, time.Duration, error) {
	created = strings.TrimSpace(created)
	if created == "" {
		return freshnessStale, 0, fmt.Errorf("a imagem de dados não tem o label %s", SecurityDataLabel)
	}
	at, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return freshnessStale, 0, fmt.Errorf("label %s ilegível (%q): %w", SecurityDataLabel, created, err)
	}
	age := now.Sub(at)
	switch {
	case age > failAfter:
		return freshnessStale, age, nil
	case age > warnAfter:
		return freshnessWarn, age, nil
	}
	return freshnessOK, age, nil
}

// SecurityData puxa a imagem com a base NVD do Dependency-Check e devolve o diretório de dados,
// para o scan rodar com autoUpdate desligado.
//
// A base é publicada por um job dedicado (update-security-data do orchestrator); o scan não fala
// com a API da NVD. A idade vem do label org.opencontainers.image.created: acima de
// warnAfterHours o scan avisa e segue; acima de failAfterHours é falha técnica, com mensagem
// que não se confunde com achado de CVE.
//
// Imagem ausente, 401 ou label ilegível também são erro -- quem chama só chega aqui com
// credencial, e cair em silêncio para o download completo é o que esta função existe para evitar.
//
// +cache="never"
func (u *OrchestratorUtils) SecurityData(
	ctx context.Context,
	// Imagem com a base, ex: basis-registry.basis.com.br/basis/iac/nvd-data/odc-data:13.
	image string,
	// Host do registro a que a credencial pertence.
	registryHost string,
	// Usuário do registro.
	registryUser string,
	// Senha do registro.
	registryPassword *dagger.Secret,
	// Idade, em horas, a partir da qual o scan avisa e segue.
	// +default=48
	warnAfterHours int,
	// Idade, em horas, a partir da qual o scan falha.
	// +default=168
	failAfterHours int,
) (*dagger.Directory, error) {
	if registryPassword == nil {
		return nil, fmt.Errorf("SecurityData precisa da credencial do registro")
	}
	ctr := dag.Container().
		WithRegistryAuth(registryHost, registryUser, registryPassword).
		From(image)
	return securityDataFromContainer(ctx, image, ctr, time.Now(), warnAfterHours, failAfterHours)
}

// securityDataFromContainer é o miolo de SecurityData, separado do pull para o teste de engine
// montar a imagem sem registro.
func securityDataFromContainer(ctx context.Context, image string, ctr *dagger.Container, now time.Time, warnAfterHours, failAfterHours int) (*dagger.Directory, error) {
	created, err := ctr.Label(ctx, SecurityDataLabel)
	if err != nil {
		return nil, fmt.Errorf("falha técnica ao ler a imagem de dados de segurança %s: %w", image, err)
	}
	freshness, age, err := classifySecurityData(created, now,
		time.Duration(warnAfterHours)*time.Hour, time.Duration(failAfterHours)*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("falha técnica na base de segurança %s: %w", image, err)
	}
	hours := int(age.Hours())
	switch freshness {
	case freshnessStale:
		return nil, fmt.Errorf("falha técnica: a base NVD %s tem %dh (limite %dh); o job de atualização do nvd-data está quebrado -- isto não é achado de CVE",
			image, hours, failAfterHours)
	case freshnessWarn:
		fmt.Printf("⚠️  A base NVD %s tem %dh (aviso a partir de %dh, falha a partir de %dh). Verifique o job de atualização do nvd-data.\n",
			image, hours, warnAfterHours, failAfterHours)
	default:
		fmt.Printf("🗄️  Base NVD %s com %dh.\n", image, hours)
	}
	return ctr.Directory(SecurityDataDir), nil
}

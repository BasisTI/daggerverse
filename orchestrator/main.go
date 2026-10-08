// Orchestrator genérico de CI/CD dirigido por um arquivo declarativo.
//
// Todo o comportamento vem de um `ci/pipeline.toml` do projeto consumidor: os
// targets declarados lá viram simultaneamente as estratégias de build, os checks
// de qualidade e o mapa de imagens usado por CheckImages/Promote. Isso torna
// impossível o drift entre "o que se builda" e "o que se promove", e dispensa o
// módulo Dagger por projeto que existia até a versão 2.x.
package main

import (
	"context"
	"dagger/orchestrator/internal/dagger"
	"fmt"
	"sort"
	"strings"

	"github.com/BasisTI/daggerverse/gitlabci"
	"github.com/BasisTI/daggerverse/pipeline"
	"github.com/BasisTI/daggerverse/pipeline/config"
)

// Orchestrator expõe as funções de pipeline dirigidas pela configuração
// declarativa do projeto.
type Orchestrator struct {
	// Source é a raiz do repositório do projeto consumidor.
	Source *dagger.Directory
	// ConfigPath é o caminho do arquivo declarativo, relativo à raiz.
	ConfigPath string
}

// New constrói o orchestrator a partir da raiz do repositório e do caminho do
// arquivo de configuração.
func New(
	// Diretório raiz do repositório do projeto.
	// +defaultPath="."
	source *dagger.Directory,
	// Caminho do arquivo declarativo de pipeline, relativo à raiz do repositório.
	// +default="ci/pipeline.toml"
	configPath string,
) *Orchestrator {
	return &Orchestrator{Source: source, ConfigPath: configPath}
}

// loadConfig lê e valida o arquivo declarativo do projeto.
func (o *Orchestrator) loadConfig(ctx context.Context) (*config.Config, error) {
	contents, err := o.Source.File(o.ConfigPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("não foi possível ler %s: %w", o.ConfigPath, err)
	}
	cfg, err := config.Load([]byte(contents))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", o.ConfigPath, err)
	}
	return cfg, nil
}

// Validate carrega e valida o arquivo declarativo, devolvendo um relatório
// legível com os targets resolvidos e o mapa de imagens derivado.
//
// Não faz nenhuma chamada de rede: é o job de lint da configuração.
func (o *Orchestrator) Validate(ctx context.Context) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	return renderReport(cfg, o.ConfigPath), nil
}

// SonarProjectKeys devolve, uma por linha, as chaves de projeto no SonarQube dos
// targets com `sonar = true`.
//
// Existe para o provisionamento: num servidor SonarQube vazio, a primeira
// análise de um projeto é sempre de merge request, e o plugin de branch valida a
// branch base ANTES de o servidor auto-provisionar o projeto -- a análise morre
// com "No branch exists in Sonarqube with the name main" e o projeto sequer é
// criado. Os projetos precisam existir antes da primeira MR, e esta função é a
// fonte da lista, para que ela não seja duplicada fora do pipeline.toml.
func (o *Orchestrator) SonarProjectKeys(ctx context.Context) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	return strings.Join(cfg.SonarProjectKeys(), "\n"), nil
}

// CheckQuality roda testes e análise estática (Sonar) nos targets alterados que
// declaram `sonar = true`.
func (o *Orchestrator) CheckQuality(
	ctx context.Context,
	// Branch base para comparar as mudanças (ex: "origin/develop").
	baseBranch string,
	// Digest do commit atual (sha completo) para reporting de status.
	commitSha string,
	// URL do SonarQube.
	sonarHost string,
	// Token de autenticação do SonarQube.
	sonarToken *dagger.Secret,
	// Se true, para no primeiro target que falhar.
	// +default=false
	stopOnFirstFail bool,
	// URL base do GitLab (ex: CI_SERVER_URL). Se vazio, commit statuses não são reportados.
	// +optional
	gitlabHost string,
	// Token com scope `api` para autenticação no GitLab.
	// +optional
	gitlabToken *dagger.Secret,
	// ID numérico do projeto no GitLab (ex: CI_PROJECT_ID).
	// +optional
	gitlabProjectId string,
	// Branch da pipeline que está reportando (ex: CI_COMMIT_REF_NAME).
	//
	// Sem ela o GitLab anexa cada commit status à pipeline mais recente do SHA. Se
	// uma segunda pipeline nascer no mesmo commit durante o build -- um push em
	// develop que também é head de uma MR, por exemplo --, o status terminal vai
	// para a pipeline nova e a original trava em "running" para sempre.
	// +optional
	gitlabRef string,
	// IID da merge request (ex: CI_MERGE_REQUEST_IID). Junto com as duas branches seguintes,
	// transforma a análise em análise de Pull Request: o código novo passa a ser o diff contra a
	// base, em vez do período de new code do projeto, e o SonarQube decora a MR no GitLab.
	//
	// Os três são necessários. Faltando qualquer um, roda análise de branch -- que é o certo fora
	// de uma MR, e é o que acontece no job de develop.
	// +optional
	mergeRequestId string,
	// Branch de origem da merge request (ex: CI_MERGE_REQUEST_SOURCE_BRANCH_NAME).
	// +optional
	mergeRequestSourceBranch string,
	// Branch de destino da merge request (ex: CI_MERGE_REQUEST_TARGET_BRANCH_NAME).
	// +optional
	mergeRequestTargetBranch string,
	// Branch do SonarQube onde gravar a análise (ex: "develop"). Transforma a execução em análise
	// de branch: o servidor passa a manter o histórico e o Overall Code daquela branch.
	//
	// Sem ela, uma execução fora de merge request grava na branch principal do projeto, seja qual
	// for a branch do git analisada. Mutuamente exclusiva com os parâmetros de merge request.
	// +optional
	sonarBranch string,
	// Analisa todos os targets com sonar = true, ignorando a detecção de mudanças.
	//
	// É o que a varredura completa de uma branch precisa: fora de uma merge request não existe
	// base natural para o diff, e sem isso a execução termina verde sem ter analisado nada.
	// +default=false
	allTargets bool,
	// Chave da API do NVD, exportada como NVD_API_KEY no container de build.
	//
	// O dependency-check-maven 13+ recusa consultar o NVD sem chave e aborta o goal, derrubando o
	// `mvn verify` inteiro. O container é hermético: uma variável do job do GitLab não chega nele,
	// então a chave precisa ser entregue explicitamente, como o token do Sonar.
	//
	// Opcional: projeto sem o plugin não passa nada e nada muda.
	// +optional
	nvdApiKey *dagger.Secret,
) error {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return err
	}
	if err := errCustomTargets(cfg, "check-quality"); err != nil {
		return err
	}
	sonarExtra, err := analysisOptions(sonarBranch, mergeRequestId, mergeRequestSourceBranch, mergeRequestTargetBranch)
	if err != nil {
		return err
	}
	targets, err := qualityTargets(cfg, sonarExtra, nvdApiKey)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Println("✅ Nenhum target com sonar = true. Nada a verificar.")
		return nil
	}
	glClient, err := newGitLabClient(ctx, gitlabHost, gitlabToken, gitlabProjectId, gitlabRef)
	if err != nil {
		return err
	}
	return pipeline.CheckQuality(ctx, daggerOps(glClient), targets, o.Source,
		baseBranch, commitSha, sonarHost, sonarToken, stopOnFirstFail, allTargets)
}

// CheckQualityFromReports é o check-quality da análise de branch que reaproveita o build do publish.
//
// Para cada target alterado com `sonar = true`: se o diretório de relatórios (o que
// publish-all-with-reports devolveu) traz o target, roda só o `sonar:sonar` sobre ele -- sem
// `clean verify` e sem testes, e com o resultado dos testes e a cobertura do build original. Se
// não traz -- target npm ou uv, ou Maven que o publish não construiu --, roda o check completo de
// sempre. Sem o diretório, todos os targets seguem o caminho completo e a função é o
// check-quality com `--sonar-branch`.
//
// Não serve à merge request: lá o job é único e roda o build e a análise juntos. O quality gate
// continua sendo esperado (`sonar.qualitygate.wait=true`) nos dois caminhos.
func (o *Orchestrator) CheckQualityFromReports(
	ctx context.Context,
	// Branch base para comparar as mudanças (ex: o commit anterior do push).
	baseBranch string,
	// Digest do commit atual (sha completo).
	commitSha string,
	// URL do SonarQube.
	sonarHost string,
	// Token de autenticação do SonarQube.
	sonarToken *dagger.Secret,
	// Branch do SonarQube onde gravar a análise (ex: "develop").
	sonarBranch string,
	// O diretório devolvido por publish-all-with-reports. Opcional: sem ele, nada é reaproveitado.
	// +optional
	reports *dagger.Directory,
	// Se true, para no primeiro target que falhar.
	// +default=false
	stopOnFirstFail bool,
	// Chave da API do NVD, usada só pelos targets que caem no check completo.
	// +optional
	nvdApiKey *dagger.Secret,
) error {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return err
	}
	if err := errCustomTargets(cfg, "check-quality-from-reports"); err != nil {
		return err
	}
	sonarExtra, err := analysisOptions(sonarBranch, "", "", "")
	if err != nil {
		return err
	}
	targets, err := qualityTargets(cfg, sonarExtra, nvdApiKey)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Println("✅ Nenhum target com sonar = true. Nada a verificar.")
		return nil
	}
	available, err := reusableTargets(ctx, reports)
	if err != nil {
		return err
	}
	var validate reuseValidator
	if reports != nil {
		validate = manifestValidator(cfg, reports, commitSha, nvdApiKey)
	}
	reused, fallback, err := withReusedReports(ctx, cfg, targets, available, reports, validate, sonarExtra, nvdApiKey)
	if err != nil {
		return err
	}
	if len(reused) > 0 {
		fmt.Printf("♻️  Relatórios do publish reaproveitados (sem clean verify, sem testes): %v\n", reused)
	}
	if len(fallback) > 0 {
		for _, f := range fallback {
			fmt.Printf("🔁 %s: check completo (clean verify) -- %s\n", f.name, f.reason)
		}
	}
	return pipeline.CheckQuality(ctx, daggerOps(nil), targets, o.Source,
		baseBranch, commitSha, sonarHost, sonarToken, stopOnFirstFail, false)
}

// analysisOptions decide o modo da análise e devolve as opções `-Dsonar.*` correspondentes,
// anunciando no log qual modo foi escolhido.
//
// São três modos, e o log importa: os dois primeiros gravam onde se espera, o terceiro grava na
// branch principal do projeto -- correto quando é de fato a principal que está sendo analisada, e
// silenciosamente errado em qualquer outro caso. Daí o aviso.
func analysisOptions(sonarBranch, mergeRequestId, mergeRequestSourceBranch, mergeRequestTargetBranch string) ([]string, error) {
	hasMergeRequest := mergeRequestId != "" || mergeRequestSourceBranch != "" || mergeRequestTargetBranch != ""
	if sonarBranch != "" && hasMergeRequest {
		return nil, fmt.Errorf(
			"--sonar-branch e os parâmetros de merge request são modos de análise mutuamente "+
				"exclusivos: recebi --sonar-branch %q junto de (id=%q, source=%q, target=%q). "+
				"Numa merge request use só os parâmetros de MR; numa varredura de branch, só o "+
				"--sonar-branch",
			sonarBranch, mergeRequestId, mergeRequestSourceBranch, mergeRequestTargetBranch)
	}

	if sonarBranch != "" {
		fmt.Printf("🌿 Análise de branch: %s\n", sonarBranch)
		return branchOptions(sonarBranch), nil
	}

	if prOptions := pullRequestOptions(mergeRequestId, mergeRequestSourceBranch, mergeRequestTargetBranch); prOptions != nil {
		fmt.Printf("🔀 Análise de Pull Request !%s (%s → %s)\n",
			mergeRequestId, mergeRequestSourceBranch, mergeRequestTargetBranch)
		return prOptions, nil
	}

	fmt.Println("⚠️  Sem --sonar-branch e sem parâmetros de merge request: a análise será gravada " +
		"na branch principal do projeto no SonarQube.")
	return nil, nil
}

// SecurityCheck roda a varredura de segurança de todos os targets Maven do repositório e
// devolve os relatórios, um subdiretório por target.
//
// É irmã de CheckQuality, com três diferenças deliberadas:
//
//  1. Sem Sonar. A varredura verifica dependências, não código; o gate de qualidade já roda
//     na merge request e na develop.
//  2. Sem detecção de mudanças -- roda sempre em todos os targets. O que ela procura não
//     está no diff: a base de CVE do NVD muda sozinha, e um target parado há meses é
//     justamente o que tem mais chance de ter apodrecido. Detecção de mudanças aqui faria
//     a varredura terminar verde sem ter varrido nada.
//  3. Sem commit status no GitLab. Ela nasce de agendamento, não de um commit que alguém
//     está esperando para mergear.
//
// A chave do NVD é obrigatória e não opcional como em CheckQuality: sem ela o
// dependency-check-maven 13+ aborta, e uma varredura de segurança que não consegue
// consultar a base não tem por que rodar.
//
// Target que falha NÃO vira erro da função. Uma função Dagger que devolve erro não devolve
// o diretório, e o `export` encadeado não teria o que exportar -- o relatório do target que
// falhou, justamente o que interessa, iria embora com o container. Em vez disso o resultado
// de cada target fica escrito no diretório:
//
//	<target>/exit-code                                    código de saída do `mvn verify`
//	<target>/<módulo>/target/dependency-check-report.html  um por módulo buildado, com o
//	<target>/<módulo>/target/dependency-check-report.json  caminho que ele tem no repositório
//	FAILED                                               só existe se algum target falhou; lista os que falharam
//	index.html, summary.json                             visão geral, uma linha por relatório
//
// O diretório passa por orchestrator-utils.SecurityReportIndex, que escreve o índice e tira os
// relatórios sem dependências (o do pom pai que o -am põe no reactor).
//
// e cabe a quem chama transformar o FAILED em falha -- o template do CI faz isso depois do
// export. Erro da função fica reservado ao que impede a varredura de começar (config
// inválida, target custom) ou de terminar (falha do engine).
func (o *Orchestrator) SecurityCheck(
	ctx context.Context,
	// Chave da API do NVD, exportada como NVD_API_KEY no container de build.
	nvdApiKey *dagger.Secret,
	// Se true, para no primeiro target que falhar. Os targets seguintes ficam sem relatório.
	// +default=false
	stopOnFirstFail bool,
) (*dagger.Directory, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := errCustomTargets(cfg, "security-check"); err != nil {
		return nil, err
	}
	targets, err := securityTargets(cfg, nvdApiKey)
	if err != nil {
		return nil, err
	}
	out := dag.Directory()
	if len(targets) == 0 {
		fmt.Println("✅ Nenhum target Maven. Nada a varrer.")
		return out, nil
	}
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.name
	}
	fmt.Printf("🎯 Targets para security check: %v\n", names)
	var failed []string
	for _, t := range targets {
		fmt.Printf("🔍 [Security] Varrendo: %s\n", t.name)
		reports, exitCode, err := t.scan(ctx, o.Source)
		if err != nil {
			return nil, fmt.Errorf("varredura de %s: %w", t.name, err)
		}
		out = out.WithDirectory(t.name, reports).
			WithNewFile(t.name+"/exit-code", fmt.Sprintf("%d\n", exitCode))
		if exitCode != 0 {
			failed = append(failed, t.name)
			fmt.Printf("❌ [Security] Falhou: %s (exit %d)\n", t.name, exitCode)
			if stopOnFirstFail {
				break
			}
			continue
		}
		fmt.Printf("✅ [Security] OK: %s\n", t.name)
	}
	if len(failed) > 0 {
		out = out.WithNewFile("FAILED", strings.Join(failed, "\n")+"\n")
	}
	return dag.OrchestratorUtils().SecurityReportIndex(out), nil
}

// PublishAll constrói e publica as imagens de todos os targets alterados e, se
// gitRemoteUrl for informado, commita os arquivos de versão bumpados.
//
// Retorna a lista de imagens publicadas, uma por linha.
func (o *Orchestrator) PublishAll(
	ctx context.Context,
	// Branch base para comparar as mudanças (ex: "origin/develop").
	baseBranch string,
	// Digest do commit atual (sha completo).
	commitSha string,
	// Versão da aplicação no formato CalVer.
	version string,
	// Registry Docker para onde as imagens serão publicadas.
	registry string,
	// Nome de usuário para autenticação no registry.
	registryUser string,
	// Senha do usuário para autenticação no registry.
	registryPassword *dagger.Secret,
	// URL base do GitLab (ex: CI_SERVER_URL). Se vazio, commit statuses não são reportados.
	// +optional
	gitlabHost string,
	// Token com scope `api` para autenticação no GitLab.
	// +optional
	gitlabToken *dagger.Secret,
	// ID numérico do projeto no GitLab (ex: CI_PROJECT_ID).
	// +optional
	gitlabProjectId string,
	// Branch da pipeline que está reportando (ex: CI_COMMIT_REF_NAME).
	//
	// Sem ela o GitLab anexa cada commit status à pipeline mais recente do SHA. Se
	// uma segunda pipeline nascer no mesmo commit durante o build -- um push em
	// develop que também é head de uma MR, por exemplo --, o status terminal vai
	// para a pipeline nova e a original trava em "running" para sempre.
	// +optional
	gitlabRef string,
	// URL do repositório Git com credenciais para push das versões bumpadas.
	// Se vazio, o bump de versão não é commitado.
	// +optional
	gitRemoteUrl string,
	// Branch Git para push das versões bumpadas.
	// +optional
	// +default="develop"
	gitBranch string,
	// Chave da API do NVD, exportada como NVD_API_KEY no container de build.
	//
	// O dependency-check-maven 13+ recusa consultar o NVD sem chave e aborta o goal, derrubando o
	// `mvn verify` inteiro. O container é hermético: uma variável do job do GitLab não chega nele,
	// então a chave precisa ser entregue explicitamente, como o token do Sonar.
	//
	// Opcional: projeto sem o plugin não passa nada e nada muda.
	// +optional
	nvdApiKey *dagger.Secret,
) (string, error) {
	return o.publishAll(ctx, nil, publishArgs{
		baseBranch: baseBranch, commitSha: commitSha, version: version,
		registry: registry, registryUser: registryUser, registryPassword: registryPassword,
		gitlabHost: gitlabHost, gitlabToken: gitlabToken, gitlabProjectId: gitlabProjectId, gitlabRef: gitlabRef,
		gitRemoteUrl: gitRemoteUrl, gitBranch: gitBranch, nvdApiKey: nvdApiKey,
	})
}

// PublishAllWithReports faz o mesmo que PublishAll e devolve, além das imagens, o que a análise do
// Sonar reaproveita do build.
//
// Existe para o job de análise de branch da develop não repetir o `mvn clean verify` que o publish
// acabou de rodar. O diretório devolvido tem:
//
//	published.txt   as imagens publicadas, uma por linha (o que PublishAll devolve)
//	<target>/       um por target Maven com sonar = true que foi construído: classes, test-classes,
//	                relatórios do surefire e do failsafe, XML do JaCoCo e fontes geradas, com os
//	                caminhos que têm no target/ do módulo
//
// O consumidor é check-quality-from-reports. É função à parte, e não um parâmetro de PublishAll,
// porque o tipo de retorno muda -- e projeto fixado numa versão antiga do template continua
// chamando PublishAll como sempre.
//
// A publicação e o bump de versão acontecem dentro da função, antes de o diretório ser devolvido:
// exportá-lo depois não repete nenhum efeito.
func (o *Orchestrator) PublishAllWithReports(
	ctx context.Context,
	// Branch base para comparar as mudanças (ex: "origin/develop").
	baseBranch string,
	// Digest do commit atual (sha completo).
	commitSha string,
	// Versão da aplicação no formato CalVer.
	version string,
	// Registry Docker para onde as imagens serão publicadas.
	registry string,
	// Nome de usuário para autenticação no registry.
	registryUser string,
	// Senha do usuário para autenticação no registry.
	registryPassword *dagger.Secret,
	// URL base do GitLab (ex: CI_SERVER_URL). Se vazio, commit statuses não são reportados.
	// +optional
	gitlabHost string,
	// Token com scope `api` para autenticação no GitLab.
	// +optional
	gitlabToken *dagger.Secret,
	// ID numérico do projeto no GitLab (ex: CI_PROJECT_ID).
	// +optional
	gitlabProjectId string,
	// Branch da pipeline que está reportando (ex: CI_COMMIT_REF_NAME).
	// +optional
	gitlabRef string,
	// URL do repositório Git com credenciais para push das versões bumpadas.
	// Se vazio, o bump de versão não é commitado.
	// +optional
	gitRemoteUrl string,
	// Branch Git para push das versões bumpadas.
	// +optional
	// +default="develop"
	gitBranch string,
	// Chave da API do NVD, exportada como NVD_API_KEY no container de build.
	// +optional
	nvdApiKey *dagger.Secret,
) (*dagger.Directory, error) {
	collect := newReportCollector()
	published, err := o.publishAll(ctx, collect, publishArgs{
		baseBranch: baseBranch, commitSha: commitSha, version: version,
		registry: registry, registryUser: registryUser, registryPassword: registryPassword,
		gitlabHost: gitlabHost, gitlabToken: gitlabToken, gitlabProjectId: gitlabProjectId, gitlabRef: gitlabRef,
		gitRemoteUrl: gitRemoteUrl, gitBranch: gitBranch, nvdApiKey: nvdApiKey,
	})
	if err != nil {
		return nil, err
	}
	return collect.directory(published), nil
}

// publishArgs agrupa os parâmetros compartilhados por PublishAll e PublishAllWithReports.
type publishArgs struct {
	baseBranch, commitSha, version         string
	registry, registryUser                 string
	registryPassword                       *dagger.Secret
	gitlabHost, gitlabProjectId, gitlabRef string
	gitlabToken                            *dagger.Secret
	gitRemoteUrl, gitBranch                string
	nvdApiKey                              *dagger.Secret
}

// publishAll é o corpo de PublishAll. Com collect não-nil, os builds Maven registram nele o que a
// análise reaproveita.
func (o *Orchestrator) publishAll(ctx context.Context, collect *reportCollector, a publishArgs) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	if err := errCustomTargets(cfg, "publish-all"); err != nil {
		return "", err
	}
	targets, err := buildTargets(cfg, a.nvdApiKey, collect)
	if err != nil {
		return "", err
	}
	glClient, err := newGitLabClient(ctx, a.gitlabHost, a.gitlabToken, a.gitlabProjectId, a.gitlabRef)
	if err != nil {
		return "", err
	}

	result, err := pipeline.PublishAll(ctx, daggerOps(glClient), targets, o.Source,
		a.baseBranch, a.commitSha, a.version, a.registry, a.registryUser, a.registryPassword)
	if err != nil {
		return "", err
	}

	// O relatório vem antes do bump: o bump reescreve os arquivos de versão e empurra um commit
	// novo, e é o commitSha de entrada que localiza a MR de origem.
	glClient.ReportPublishedImages(a.commitSha, result.Published, a.version)

	// Commit e push das versões bumpadas de volta ao repositório. A supressão do
	// pipeline vem do push option `-o ci.skip` dentro do orchestrator-utils, e
	// não de um prefixo "[skip ci]" na mensagem.
	if result.Published != "" && a.gitRemoteUrl != "" && len(result.VersionFiles) > 0 {
		commitMsg := fmt.Sprintf("Bump versão para %s", a.version)
		if err := dag.OrchestratorUtils().BumpAndCommitVersions(
			ctx, o.Source, result.VersionFiles, result.VersionFileTypes, a.version, commitMsg, a.gitBranch, a.gitRemoteUrl,
		); err != nil {
			return "", fmt.Errorf("falha ao commitar as versões bumpadas: %w", err)
		}
	}

	return result.Published, nil
}

// CheckImages verifica se todas as imagens derivadas da configuração existem no
// registry antes da promoção.
//
// Targets custom NÃO são ignorados: suas imagens existem no registry e precisam
// ser verificadas como qualquer outra.
func (o *Orchestrator) CheckImages(
	ctx context.Context,
	// URL do registry Docker.
	registry string,
	// Usuário para autenticação no registry.
	registryUser string,
	// Senha para autenticação no registry.
	registryPassword *dagger.Secret,
	// Ref Git da branch onde as imagens foram construídas (ex: "origin/develop").
	// +optional
	// +default="origin/develop"
	buildBranch string,
) error {
	imagesJson, err := o.projectImagesJson(ctx)
	if err != nil {
		return err
	}
	return dag.OrchestratorUtils().CheckImages(ctx, o.Source, imagesJson, registry,
		dagger.OrchestratorUtilsCheckImagesOpts{
			RegistryUser: registryUser,
			RegistryPass: registryPassword,
			BuildBranch:  buildBranch,
		})
}

// Promote promove as imagens derivadas da configuração de staging para produção.
//
// Assim como CheckImages, inclui as imagens de targets custom.
//
// Retorna as refs promovidas, uma por linha, e -- quando a configuração do GitLab é informada --
// comenta na MR de origem do commit o que entrou em produção.
func (o *Orchestrator) Promote(
	ctx context.Context,
	// Registry de origem (staging).
	srcRegistry string,
	// Usuário para autenticação nos registries.
	registryUser string,
	// Senha para autenticação nos registries.
	registryPassword *dagger.Secret,
	// Registry de destino (produção). Se vazio, usa srcRegistry.
	// +optional
	dstRegistry string,
	// Ref Git da branch onde as imagens foram construídas (ex: "origin/develop").
	// +optional
	// +default="origin/develop"
	buildBranch string,
	// Digest do commit atual (ex: CI_COMMIT_SHA). É por ele que a MR de develop→main é
	// localizada para receber o relatório -- o job roda depois do merge, sem CI_MERGE_REQUEST_IID
	// no ambiente. Se vazio, nada é comentado.
	// +optional
	commitSha string,
	// URL base do GitLab (ex: CI_SERVER_URL). Se vazio, o relatório não é publicado.
	// +optional
	gitlabHost string,
	// Token com scope `api` para autenticação no GitLab.
	// +optional
	gitlabToken *dagger.Secret,
	// ID numérico do projeto no GitLab (ex: CI_PROJECT_ID).
	// +optional
	gitlabProjectId string,
) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	imagesJson, err := cfg.ProjectImagesJSON()
	if err != nil {
		return "", err
	}
	glClient, err := newGitLabClient(ctx, gitlabHost, gitlabToken, gitlabProjectId, "")
	if err != nil {
		return "", err
	}

	promoted, err := dag.OrchestratorUtils().Promote(ctx, o.Source, imagesJson, srcRegistry, registryUser, registryPassword,
		dagger.OrchestratorUtilsPromoteOpts{
			DstRegistry: dstRegistry,
			BuildBranch: buildBranch,
		})
	if err != nil {
		return "", err
	}

	// As que não foram copiadas já estavam em produção: promote percorre TODAS as imagens do
	// projeto e só falha ou copia, então a subtração cobre o resto sem um segundo canal de retorno.
	unchanged := len(cfg.ProjectImages()) - countRefs(promoted)
	glClient.ReportPromotedImages(commitSha, promoted, unchanged)

	return promoted, nil
}

// countRefs conta as refs devolvidas por Promote, uma por linha.
func countRefs(promoted string) int {
	promoted = strings.TrimSpace(promoted)
	if promoted == "" {
		return 0
	}
	return len(strings.Split(promoted, "\n"))
}

// projectImagesJson devolve o mapa imagem->path derivado da configuração, no
// formato aceito pelo orchestrator-utils.
func (o *Orchestrator) projectImagesJson(ctx context.Context) (string, error) {
	cfg, err := o.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	return cfg.ProjectImagesJSON()
}

// daggerOps liga as callbacks genéricas da lib pipeline ao orchestrator-utils.
func daggerOps(gitlabClient *gitlabci.Client) pipeline.DaggerOps[*dagger.Directory, *dagger.Secret] {
	return pipeline.DaggerOps[*dagger.Directory, *dagger.Secret]{
		GetChangedProjects: func(ctx context.Context, source *dagger.Directory, baseBranch, pathsJson string) ([]string, error) {
			return dag.OrchestratorUtils().GetChangedProjects(ctx, source, baseBranch, pathsJson)
		},
		GetSubDirectory: getSubDirectory,
		TagWithSha: func(ctx context.Context, published, commitSha string, registryUser string, registryPassword *dagger.Secret) error {
			return dag.OrchestratorUtils().TagWithSha(ctx, published, commitSha, registryUser, registryPassword)
		},
		GitLabClient: gitlabClient,
	}
}

// getSubDirectory devolve o subdiretório montado no build.
//
// RepoRoot (".") significa "monte a raiz": é assim que builds reactor e
// workspaces uv/monorepo enxergam o repositório inteiro.
func getSubDirectory(source *dagger.Directory, path string) *dagger.Directory {
	if path == "" || path == pipeline.RepoRoot {
		return source
	}
	return source.Directory(path)
}

// newGitLabClient monta o cliente de commit statuses, ou (nil, nil) quando a
// configuração do GitLab não foi informada.
func newGitLabClient(ctx context.Context, gitlabHost string, gitlabToken *dagger.Secret, gitlabProjectID, gitlabRef string) (*gitlabci.Client, error) {
	if gitlabHost == "" || gitlabToken == nil || gitlabProjectID == "" {
		return nil, nil
	}
	token, err := gitlabToken.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get gitlab token: %w", err)
	}
	return &gitlabci.Client{
		BaseURL:   gitlabHost,
		Token:     token,
		ProjectID: gitlabProjectID,
		Ref:       gitlabRef,
	}, nil
}

// renderReport monta o relatório textual devolvido por Validate.
func renderReport(cfg *config.Config, configPath string) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "✅ %s é válido (schema-version %d)\n", configPath, cfg.SchemaVersion)
	fmt.Fprintf(&sb, "Grupo: %s\n", cfg.Project.Group)
	fmt.Fprintf(&sb, "\nTargets (%d):\n", len(cfg.Targets))

	for _, rt := range cfg.ResolveAll() {
		fmt.Fprintf(&sb, "\n  %s\n", rt.Name)
		fmt.Fprintf(&sb, "    tipo:         %s\n", rt.Type)
		fmt.Fprintf(&sb, "    path:         %s\n", rt.Path)
		fmt.Fprintf(&sb, "    source-path:  %s\n", rt.SourcePath)
		fmt.Fprintf(&sb, "    imagem:       %s\n", cfg.ImagePath(rt.Name))
		fmt.Fprintf(&sb, "    version-file: %s\n", orDash(versionFilePath(rt)))
		fmt.Fprintf(&sb, "    sonar:        %t\n", rt.Sonar)
		if rt.Sonar {
			fmt.Fprintf(&sb, "    sonar-key:    %s\n", rt.SonarProjectKey)
		}
		if rt.QualityType != rt.Type {
			fmt.Fprintf(&sb, "    quality-type: %s\n", rt.QualityType)
		}
		if len(rt.ExtraTriggerPaths) > 0 {
			fmt.Fprintf(&sb, "    triggers:     %s\n", strings.Join(rt.ExtraTriggerPaths, ", "))
		}
		renderTypeFields(&sb, rt, rt.Type)
		// Quando o código é analisado por outro build system, os campos que o
		// check de qualidade vai usar também precisam aparecer no relatório.
		if rt.QualityType != rt.Type {
			fmt.Fprintf(&sb, "    quality (%s):\n", rt.QualityType)
			renderTypeFields(&sb, rt, rt.QualityType)
		}
	}

	images := cfg.ProjectImages()
	refs := make([]string, 0, len(images))
	for image := range images {
		refs = append(refs, image)
	}
	sort.Strings(refs)
	fmt.Fprintf(&sb, "\nImagens derivadas (%d) — usadas por check-images e promote:\n", len(refs))
	for _, image := range refs {
		fmt.Fprintf(&sb, "  %s -> %s\n", image, strings.Join(images[image], ", "))
	}

	if warnings := configWarnings(cfg); len(warnings) > 0 {
		sb.WriteString("\n⚠️  Avisos:\n")
		for _, w := range warnings {
			fmt.Fprintf(&sb, "  - %s\n", w)
		}
	}

	return sb.String()
}

// renderTypeFields escreve os campos específicos de um tipo de build. É chamado
// para o tipo de build do target e, quando divergente, para seu tipo de quality.
func renderTypeFields(sb *strings.Builder, rt config.ResolvedTarget, kind config.TargetType) {
	switch kind {
	case config.TypeMaven:
		fmt.Fprintf(sb, "    maven-image:  %s\n", orDash(rt.MavenImage))
		fmt.Fprintf(sb, "    use-docker:   %t\n", rt.UseDocker)
		if rt.Reactor {
			fmt.Fprintf(sb, "    reactor:      true (module %s)\n", rt.Module)
		}
		if len(rt.ExtraOptions) > 0 {
			fmt.Fprintf(sb, "    extra-opts:   %s\n", strings.Join(rt.ExtraOptions, " "))
		}
	case config.TypeNpm:
		fmt.Fprintf(sb, "    build-image:  %s\n", orDash(rt.NpmBuildImage))
		fmt.Fprintf(sb, "    run-image:    %s\n", orDash(rt.NpmRunImage))
	case config.TypeUv:
		fmt.Fprintf(sb, "    build-image:  %s\n", orDash(rt.UvBuildImage))
		fmt.Fprintf(sb, "    run-image:    %s\n", orDash(rt.UvRunImage))
		fmt.Fprintf(sb, "    run-subdir:   %s\n", orDash(rt.RunSubdir))
		if len(rt.Customizations) > 0 {
			fmt.Fprintf(sb, "    customiz.:    %s\n", strings.Join(rt.Customizations, ", "))
		}
	case config.TypeDockerfile:
		fmt.Fprintf(sb, "    dockerfile:   %s\n", rt.Dockerfile)
	}
}

// configWarnings lista o que é válido no schema mas o orchestrator genérico não
// consegue executar. São avisos, não erros: a configuração continua sendo a
// fonte de verdade de check-images e promote, que funcionam para todos os
// targets, inclusive os custom.
//
// `sonar = true` em target dockerfile não entra aqui: ou o target declara
// `quality-type` e o check roda de verdade, ou a configuração nem carrega —
// virou erro de validação em config.Validate.
func configWarnings(cfg *config.Config) []string {
	var warnings []string

	if custom := customTargetNames(cfg); len(custom) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"targets custom (%s): publish-all e check-quality do módulo genérico falham para eles; "+
				"o projeto precisa de um módulo Dagger próprio para buildá-los. "+
				"check-images e promote continuam cobrindo suas imagens normalmente",
			strings.Join(custom, ", ")))
	}

	return warnings
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

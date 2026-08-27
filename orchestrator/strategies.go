package main

import (
	"context"
	"dagger/orchestrator/internal/dagger"
	"fmt"
	"sort"

	"github.com/BasisTI/daggerverse/pipeline"
	"github.com/BasisTI/daggerverse/pipeline/config"
	"github.com/BasisTI/daggerverse/pipeline/imageref"
)

// qualityModulePath informa à estratégia onde o target vive dentro da raiz do
// repositório, que é o que o check de qualidade monta.
//
// No modo reactor o próprio `module` já é o path dentro do reactor e a montagem
// da raiz é implícita, então não há nada a informar.
func qualityModulePath(rt config.ResolvedTarget, sourcePath string) string {
	if rt.Reactor {
		return ""
	}
	return moduleSubpath(sourcePath)
}

// moduleSubpath normaliza o path do target para uso como subdiretório do mount:
// a raiz do repositório vira string vazia, que é como maven e npm expressam
// "o projeto é a própria árvore montada".
func moduleSubpath(sourcePath string) string {
	if sourcePath == pipeline.RepoRoot {
		return ""
	}
	return sourcePath
}

// pullRequestOptions monta os `-Dsonar.pullrequest.*` que transformam a análise de uma branch
// qualquer em análise de Pull Request.
//
// É o que faz o SonarQube (a) calcular código novo pelo diff contra a base, em vez do período de
// new code do projeto, e (b) ter um objeto PR onde pendurar o comentário na MR. Sem esses três
// parâmetros a análise de uma MR aterrissa na branch principal do projeto e não há o que decorar --
// o binding com o GitLab e o "monorepository support" são pré-requisitos, não gatilhos.
//
// Devolve nil se qualquer um dos três faltar: uma análise de PR pela metade é rejeitada pelo
// scanner, e cair de volta na análise de branch é o comportamento correto fora de MR.
func pullRequestOptions(key, branch, base string) []string {
	if key == "" || branch == "" || base == "" {
		return nil
	}
	return []string{
		"-Dsonar.pullrequest.key=" + key,
		"-Dsonar.pullrequest.branch=" + branch,
		"-Dsonar.pullrequest.base=" + base,
	}
}

// branchOptions monta o `-Dsonar.branch.name` que diz ao servidor em qual branch do projeto a
// análise deve ser gravada.
//
// Sem ele o scanner grava na branch principal do projeto, seja qual for a branch do git que está
// sendo analisada. Uma varredura de `develop` sem esse parâmetro aterrissa na `main` do SonarQube:
// código de integração registrado como produção, sem erro nenhum no log -- é o modo de falha que
// esta opção existe para impedir.
//
// Mutuamente exclusivo com pullRequestOptions: quem decide entre os dois é CheckQuality, que
// recusa a combinação em vez de escolher um por precedência.
func branchOptions(branch string) []string {
	if branch == "" {
		return nil
	}
	return []string{"-Dsonar.branch.name=" + branch}
}

// buildStrategy escolhe a estratégia de publish do target. As estratégias fecham
// sobre o ResolvedTarget: a lib pipeline não carrega mais nenhum campo de
// configuração opaco.
func buildStrategy(rt config.ResolvedTarget, group string) (pipeline.BuildStrategy[*dagger.Directory, *dagger.Secret], error) {
	switch rt.Type {
	case config.TypeMaven:
		return publishMaven(rt, group), nil
	case config.TypeNpm:
		return publishNpm(rt, group), nil
	case config.TypeUv:
		return publishUv(rt, group), nil
	case config.TypeDockerfile:
		return publishDockerfile(rt, group), nil
	default:
		return nil, fmt.Errorf("target %q: type %q não tem estratégia de build", rt.Name, rt.Type)
	}
}

// qualityStrategy escolhe o check de qualidade do target.
//
// O despacho é pelo tipo EFETIVO DE QUALITY, não pelo tipo de build: um target
// cuja imagem sai de um Dockerfile próprio mas cujo código é um projeto Python
// declara `quality-type = "uv"` e roda checkUv normalmente. Sem `quality-type`,
// QualityType é igual a Type e o despacho é o de sempre.
func qualityStrategy(rt config.ResolvedTarget, sonarExtra []string) (pipeline.QualityStrategy[*dagger.Directory, *dagger.Secret], error) {
	switch rt.QualityType {
	case config.TypeMaven:
		return checkMaven(rt, sonarExtra), nil
	case config.TypeNpm:
		return checkNpm(rt, sonarExtra), nil
	case config.TypeUv:
		return checkUv(rt, sonarExtra), nil
	default:
		return nil, fmt.Errorf(
			"target %q: sonar = true não é suportado em targets type = %q sem quality-type "+
				"(não há build system para rodar testes e análise estática); "+
				"declare quality-type = \"maven\" | \"npm\" | \"uv\"", rt.Name, rt.Type)
	}
}

// --- maven ---

// mavenModule é o módulo passado ao FullBuild: no modo reactor é o path do
// módulo dentro do reactor; fora dele, o próprio nome do target.
func mavenModule(rt config.ResolvedTarget, target string) string {
	if rt.Reactor {
		return rt.Module
	}
	return target
}

func mavenOpts(rt config.ResolvedTarget) dagger.MavenOpts {
	return dagger.MavenOpts{
		BuildImage:         rt.MavenImage,
		UseDocker:          rt.UseDocker,
		ReactorMode:        rt.Reactor,
		ExtraOptions:       rt.ExtraOptions,
		SonarPluginVersion: rt.SonarPluginVersion,
	}
}

// mavenSource remove os diretórios target/ antes de subir a árvore ao engine,
// como faziam os orchestrators por projeto.
func mavenSource(source *dagger.Directory) *dagger.Directory {
	return dag.Directory().WithDirectory("/", source, dagger.DirectoryWithDirectoryOpts{
		Exclude: []string{"target/", "**/target/"},
	})
}

func publishMaven(rt config.ResolvedTarget, group string) pipeline.BuildStrategy[*dagger.Directory, *dagger.Secret] {
	return func(
		ctx context.Context, source *dagger.Directory,
		module, commitSha, version, registry, registryUser string,
		registryPassword *dagger.Secret,
	) (string, error) {
		m := dag.Maven(mavenOpts(rt))
		// O DockerBuildConfig do maven recebe a referência completa; a tag fica
		// embutida nela (jib.to.image aceita ref com tag), de modo que a imagem
		// devolvida por ImageURL é exatamente a que foi publicada.
		dockerConfig := m.NewDockerBuildConfig(imageref.Ref(registry, group, rt.Image, version), "", registryUser, registryPassword)
		result := m.FullBuild(mavenSource(source), mavenModule(rt, module), commitSha, version,
			dagger.MavenFullBuildOpts{DockerConfig: dockerConfig})
		return result.ImageURL(ctx)
	}
}

func checkMaven(rt config.ResolvedTarget, sonarExtra []string) pipeline.QualityStrategy[*dagger.Directory, *dagger.Secret] {
	return func(
		ctx context.Context, source *dagger.Directory,
		module, sourcePath, sonarHost string, sonarToken *dagger.Secret,
	) error {
		m := dag.Maven(mavenOpts(rt))
		// A chave do Sonar vem do target resolvido, não do argumento `module`:
		// aqui `module` é o path do módulo no reactor, e a chave é o
		// identificador do projeto no servidor (default: o nome do target).
		sonarConfig := m.NewSonarConfig(sonarHost, sonarToken, true, sonarExtra,
			dagger.MavenNewSonarConfigOpts{ProjectKey: rt.SonarProjectKey})
		// Versão vazia: o check não publica nada e não reescreve o pom. A versão
		// declarada nele -- a última publicada -- é a que o Sonar registra.
		result := m.FullBuild(mavenSource(source), mavenModule(rt, module), "", "",
			dagger.MavenFullBuildOpts{
				SonarConfig: sonarConfig,
				ModulePath:  qualityModulePath(rt, sourcePath),
			})
		_, err := result.ImageURL(ctx)
		return err
	}
}

// --- npm ---

func npmOpts(rt config.ResolvedTarget) dagger.NpmOpts {
	return dagger.NpmOpts{
		BuildImage: rt.NpmBuildImage,
		RunImage:   rt.NpmRunImage,
	}
}

func publishNpm(rt config.ResolvedTarget, group string) pipeline.BuildStrategy[*dagger.Directory, *dagger.Secret] {
	return func(
		ctx context.Context, source *dagger.Directory,
		module, commitSha, version, registry, registryUser string,
		registryPassword *dagger.Secret,
	) (string, error) {
		n := dag.Npm(source, npmOpts(rt))
		dockerConfig := n.NewDockerBuildConfig(registry, group, rt.Image, registryUser, registryPassword)
		result := n.FullBuild(dagger.NpmFullBuildOpts{
			CommitSha:    commitSha,
			Version:      version,
			DockerConfig: dockerConfig,
		})
		return result.ImageURL(ctx)
	}
}

func checkNpm(rt config.ResolvedTarget, sonarExtra []string) pipeline.QualityStrategy[*dagger.Directory, *dagger.Secret] {
	return func(
		ctx context.Context, source *dagger.Directory,
		module, sourcePath, sonarHost string, sonarToken *dagger.Secret,
	) error {
		opts := npmOpts(rt)
		opts.ModulePath = moduleSubpath(sourcePath)
		n := dag.Npm(source, opts)
		sonarConfig := n.NewSonarConfig(sonarHost, sonarToken, rt.SonarProjectKey,
			dagger.NpmNewSonarConfigOpts{ExtraOptions: sonarExtra})
		result := n.FullBuild(dagger.NpmFullBuildOpts{SonarConfig: sonarConfig})
		_, err := result.ImageURL(ctx)
		return err
	}
}

// --- uv ---

func uvOpts(rt config.ResolvedTarget) dagger.UvOpts {
	return dagger.UvOpts{
		BuildImage:     rt.UvBuildImage,
		RunImage:       rt.UvRunImage,
		RunSubdir:      rt.RunSubdir,
		Customizations: rt.Customizations,
	}
}

func publishUv(rt config.ResolvedTarget, group string) pipeline.BuildStrategy[*dagger.Directory, *dagger.Secret] {
	return func(
		ctx context.Context, source *dagger.Directory,
		module, commitSha, version, registry, registryUser string,
		registryPassword *dagger.Secret,
	) (string, error) {
		u := dag.Uv(source, uvOpts(rt))
		dockerConfig := u.NewDockerBuildConfig(registry, group, rt.Image, registryUser, registryPassword)
		result := u.FullBuild(dagger.UvFullBuildOpts{
			CommitSha:    commitSha,
			Version:      version,
			DockerConfig: dockerConfig,
		})
		return result.ImageURL(ctx)
	}
}

func checkUv(rt config.ResolvedTarget, sonarExtra []string) pipeline.QualityStrategy[*dagger.Directory, *dagger.Secret] {
	return func(
		ctx context.Context, source *dagger.Directory,
		module, sourcePath, sonarHost string, sonarToken *dagger.Secret,
	) error {
		// TODO(scm): o módulo uv ainda ancora uv.lock e pyproject.toml na raiz do
		// Source, então receber a raiz do repositório quebraria essas leituras.
		// Até ele virar path-aware como maven e npm, o subdiretório é recortado
		// aqui -- ou seja, targets uv seguem sem .git e sem blame no Sonar.
		u := dag.Uv(source.Directory(sourcePath), uvOpts(rt))
		sonarConfig := u.NewSonarConfig(sonarHost, sonarToken, rt.SonarProjectKey,
			dagger.UvNewSonarConfigOpts{ExtraOptions: sonarExtra})
		result := u.FullBuild(dagger.UvFullBuildOpts{SonarConfig: sonarConfig})
		_, err := result.ImageURL(ctx)
		return err
	}
}

// --- dockerfile ---

// publishDockerfile builda o Dockerfile do target e publica a imagem.
//
// O Dockerfile é sempre relativo ao diretório montado: quando source-path é ".",
// o campo dockerfile precisa trazer o caminho completo a partir da raiz do repo
// (ex: "apps/judge-api/Dockerfile"), que é como o kaizenstat builda tudo.
func publishDockerfile(rt config.ResolvedTarget, group string) pipeline.BuildStrategy[*dagger.Directory, *dagger.Secret] {
	return func(
		ctx context.Context, source *dagger.Directory,
		module, commitSha, version, registry, registryUser string,
		registryPassword *dagger.Secret,
	) (string, error) {
		ref := imageref.Ref(registry, group, rt.Image, version)

		container := source.DockerBuild(dagger.DirectoryDockerBuildOpts{Dockerfile: rt.Dockerfile})
		for _, label := range ociLabels(commitSha, version) {
			container = container.WithLabel(label.key, label.value)
		}
		if registryPassword != nil {
			container = container.WithRegistryAuth(registry, registryUser, registryPassword)
		}

		addr, err := container.Publish(ctx, ref)
		if err != nil {
			return "", fmt.Errorf("falha ao publicar %s: %w", module, err)
		}
		return addr, nil
	}
}

type ociLabel struct{ key, value string }

// ociLabels ordena os labels OCI para que a imagem resultante seja determinística.
func ociLabels(commitSha, version string) []ociLabel {
	raw := imageref.OCILabels(commitSha, version)
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	labels := make([]ociLabel, 0, len(keys))
	for _, key := range keys {
		labels = append(labels, ociLabel{key: key, value: raw[key]})
	}
	return labels
}

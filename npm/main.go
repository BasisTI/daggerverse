package main

import (
	"context"
	"dagger/npm/internal/dagger"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/BasisTI/daggerverse/gitlabci"
	"github.com/BasisTI/daggerverse/pipeline/sonarargs"
)

const DefaultNpmCacheName = "npm-cache"
const DefaultWorkdir = "/app"
const DefaultBuildDir = "dist"

var NpmRumCmd = []string{"npm", "run"}

// Npm represents an npm project and provides helpers to run common build steps inside a Dagger container.
type Npm struct {
	BuildImage    string
	RunImage      string
	UseCache      bool
	Source        *dagger.Directory
	NodeContainer *dagger.Container
	// ModulePath é o caminho do projeto relativo à raiz do repositório. Quando preenchido, Source é
	// a RAIZ DO REPOSITÓRIO e não o diretório do projeto. Ver a doc do parâmetro em New.
	ModulePath string
	// RegistryUser e RegistryPassword são a credencial do registro declarado na linha `registry=`
	// do .npmrc do projeto. Ver a doc dos parâmetros em New.
	RegistryUser     string
	RegistryPassword *dagger.Secret
	// TODO: put BuildResultDir here (example: "dist")
}

// Workdir é o diretório onde os comandos rodam dentro do container: /app quando Source já é o
// projeto, /app/<ModulePath> quando Source é a raiz do repositório.
func (n *Npm) Workdir() string {
	if n.ModulePath == "" {
		return DefaultWorkdir
	}
	return DefaultWorkdir + "/" + n.ModulePath
}

// packageJSONPath é o caminho do package.json dentro de Source, que segue a mesma regra do Workdir.
func (n *Npm) packageJSONPath() string {
	if n.ModulePath == "" {
		return "package.json"
	}
	return n.ModulePath + "/package.json"
}

type packageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// New constructs an npm helper bound to the provided source directory and runtime configuration.
func New(
	// Directory with node/npm source code
	source *dagger.Directory,
	// BuildImage name for building application
	// +default="node:22.16.0-alpine3.22"
	buildImage string,
	// BuildImage name for building application
	// +default="nginx:1.27.3-alpine-slim"
	runImage string,
	// Use Npm Cache
	// +default=true
	useCache bool,
	// Caminho do projeto relativo à raiz do repositório. Quando informado, `source` é a RAIZ DO
	// REPOSITÓRIO e não o diretório do projeto: a árvore inteira é montada em /app e os comandos
	// rodam em /app/<modulePath>.
	//
	// É o que permite ao Sonar fazer blame. O .git só existe na raiz e o índice do git referencia
	// os arquivos pelo caminho a partir dela, então montar só o diretório do projeto (com ou sem o
	// .git dentro) deixa o scanner sem SCM -- e sem SCM não há análise de PR nem período de new
	// code por diff.
	// +optional
	modulePath string,
	// Usuário do registro npm declarado na linha `registry=` do .npmrc do projeto. Ver
	// registryPassword.
	// +optional
	registryUser string,
	// Senha do registro npm. Com registryUser, vira o `_auth` do npm (base64 de `usuário:senha`)
	// para o registro da linha `registry=` do .npmrc do projeto, e só nos passos que instalam
	// pacotes (`npm ci`).
	//
	// O container é hermético: o .npmrc do projeto diz onde buscar, mas um registro que exige
	// login (o Nexus, por exemplo) recusa o `npm ci` com E401 sem uma credencial entregue aqui. O
	// `_auth` é montado dentro do engine e entra como variável secreta só na execução da
	// instalação, referenciado por um userconfig temporário (`${NPM_REGISTRY_AUTH}`); os dois saem
	// do container logo depois: não ficam em camada, no cache do npm nem no diretório publicado.
	//
	// Opcional: sem ela o `npm ci` roda como sempre rodou. Com ela e sem `registry=` no .npmrc, a
	// credencial não é usada -- o registro seria o público, que não deve recebê-la.
	// +optional
	registryPassword *dagger.Secret) *Npm {
	return &Npm{
		BuildImage:       buildImage,
		RunImage:         runImage,
		UseCache:         useCache,
		Source:           source,
		ModulePath:       modulePath,
		RegistryUser:     registryUser,
		RegistryPassword: registryPassword,
	}
}

// NewContainer creates a fresh container primed with the project source and optional cache.
func (n *Npm) NewContainer() *dagger.Container {
	container := dag.Container().From(n.BuildImage)
	if n.UseCache {
		container = container.WithMountedCache("/root/.npm", dag.CacheVolume(DefaultNpmCacheName))
	}
	if n.Source != nil {
		container = container.WithDirectory(DefaultWorkdir, n.Source, dagger.ContainerWithDirectoryOpts{Exclude: []string{"**/node_modules", "**/dist"}})
	}
	return container.WithWorkdir(n.Workdir())
}

// Container returns the memoised container, initialising it if needed.
func (n *Npm) Container() *dagger.Container {
	if n.NodeContainer == nil {
		n.NodeContainer = n.NewContainer()
	}
	return n.NodeContainer
}

// GetAngularDistDir returns the Angular distribution directory produced by the build.
func (n *Npm) GetAngularDistDir() *dagger.Directory {
	return n.Container().Directory(fmt.Sprintf("%s/dist/browser", DefaultWorkdir))
}

// getProjectName reads the project name from package.json.
func (n *Npm) getProjectName(ctx context.Context) string {
	contents, err := n.Source.File(n.packageJSONPath()).Contents(ctx)
	if err != nil {
		return ""
	}
	var pkg packageJSON
	if err := json.Unmarshal([]byte(contents), &pkg); err != nil {
		return ""
	}
	return pkg.Name
}

// FullBuild executes a typical npm pipeline: install dependencies, run tests, build assets, optional Sonar analysis, and image metadata preparation.
func (n *Npm) FullBuild(ctx context.Context,
	// Git commit SHA for image labels
	// +optional
	commitSha string,
	// Application version for image tag. When empty, falls back to package.json version.
	// +optional
	version string,
	// +optional
	sonarConfig *SonarConfig,
	// +optional
	dockerConfig *DockerBuildConfig,
	// GitLab configuration for reporting commit statuses
	// +optional
	gitlabConfig *GitLabConfig,
) (*BuildResult, error) {
	if n.Source == nil {
		return nil, fmt.Errorf("source is nil")
	}

	if version != "" {
		updated, err := n.withPackageJsonVersion(ctx, version)
		if err != nil {
			return nil, fmt.Errorf("failed to set version in package.json: %w", err)
		}
		n.Source = updated
		n.NodeContainer = nil
	}

	stages := []PipelineStage{
		{DisplayName: "Install Dependencies", Command: []string{"npm", "ci"}, installsPackages: true},
		{DisplayName: "Build Production Bundle", Command: []string{"npm", "run", "build"}},
	}

	if sonarConfig != nil {
		sonarOptions, err := n.buildSonarOptions(ctx, sonarConfig)
		if err != nil {
			return nil, err
		}
		stages = append(stages,
			PipelineStage{
				DisplayName: "Run SonarQube Analysis",
				Command:     []string{"sonar-scanner"},
				Options:     sonarOptions,
				Image:       sonarConfig.Image,
				Owner:       "scanner-cli",
			})
	}
	var gitlabClient *gitlabci.Client
	if gitlabConfig != nil {
		token, err := gitlabConfig.TokenSecret.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("get gitlab token: %w", err)
		}
		gitlabClient = &gitlabci.Client{
			BaseURL:   gitlabConfig.Host,
			Token:     token,
			ProjectID: gitlabConfig.ProjectID,
			Ref:       gitlabConfig.Ref,
		}
	}

	result, err := n.executeStages(ctx, stages, gitlabClient, commitSha)
	if err != nil {
		return nil, err
	}

	if dockerConfig != nil {
		if version == "" {
			version = n.GetVersionOrDefault(ctx, "latest")
		}
		container := n.runtimeImage(result, version, commitSha).
			WithRegistryAuth(dockerConfig.Registry, dockerConfig.Username, dockerConfig.PasswordSecret)
		publishedImage, err := container.Publish(ctx, dockerConfig.imageReference(version))
		if err != nil {
			return nil, err
		}
		result.ImageUrl = publishedImage
	}

	return result, nil
}

// runtimeImage monta a imagem publicada: o RunImage com só o diretório de build. Nada do container
// de build -- node_modules, .npmrc, cache do npm -- passa para ela.
func (n *Npm) runtimeImage(result *BuildResult, version, commitSha string) *dagger.Container {
	container := result.Container.From(n.RunImage).
		WithLabel("org.opencontainers.image.version", version).
		WithLabel("org.opencontainers.image.created", time.Now().Format(time.RFC3339)).
		WithDirectory("/usr/share/nginx/html/", result.Artifacts)
	if commitSha != "" {
		container = container.WithLabel("org.opencontainers.image.revision", commitSha)
	}
	return container
}

// executeStages runs each pipeline stage sequentially while collecting logs and artifacts.
func (n *Npm) executeStages(ctx context.Context, stages []PipelineStage, gitlabClient *gitlabci.Client, commitSha string) (*BuildResult, error) {
	var statusPrefix string
	if gitlabClient != nil {
		statusPrefix = n.getProjectName(ctx)
	}
	auth, err := n.registryAuth(ctx)
	if err != nil {
		return nil, err
	}
	stageContainer := n.Container()
	result := &BuildResult{}
	for _, stage := range stages {
		statusName := ""
		if statusPrefix != "" {
			statusName = fmt.Sprintf("%s: %s", statusPrefix, stage.DisplayName)
			_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateRunning, statusName, "")
		}
		stageResult, err := n.executeStage(ctx, stageContainer, stage, auth)
		if statusName != "" {
			if err != nil {
				_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateFailed, statusName, "")
			} else {
				_ = gitlabClient.SetCommitStatus(commitSha, gitlabci.StateSuccess, statusName, "")
			}
		}
		if err != nil {
			return nil, err
		}
		stageContainer = stageResult.Container
		result.Stdout = append(result.Stdout, stageResult.Stdout)
		result.Stderr = append(result.Stderr, stageResult.Stderr)
		result.ExecutedStages = append(result.ExecutedStages, stage.DisplayName)
		result.Artifacts = stageContainer.Directory(fmt.Sprintf("%s/%s", DefaultWorkdir, DefaultBuildDir))
	}
	result.Container = stageContainer
	return result, nil
}

// executeStage mounts sources, runs the stage command, and captures logs plus dist artifacts.
func (n *Npm) executeStage(ctx context.Context, container *dagger.Container, stage PipelineStage, auth *registryAuth) (*StageResult, error) {
	cmd := stage.Command
	if len(cmd) == 0 {
		cmd = append([]string{"npm"}, stage.Goals...)
	}
	if len(stage.Options) > 0 {
		cmd = append(cmd, stage.Options...)
	}
	var stageContainer *dagger.Container
	var returnContainer *dagger.Container
	// If the PipelineStage has its own image, use it, but, return the original image.
	// Only mount n.Source for stages with a separate image (e.g. Sonar), since they
	// start from a fresh container. Regular stages reuse the previous container which
	// already has the source and any files generated by earlier stages (e.g. version.ts
	// produced by postinstall scripts).
	if stage.Image != "" {
		returnContainer = container
		stageContainer = dag.Container().From(stage.Image)
		containerOpts := dagger.ContainerWithDirectoryOpts{Exclude: []string{"**/node_modules", "**/" + DefaultBuildDir}}
		if stage.Owner != "" {
			containerOpts.Owner = stage.Owner
		}
		stageContainer = stageContainer.
			WithDirectory(DefaultWorkdir, n.Source, containerOpts).
			WithWorkdir(n.Workdir())
	} else {
		stageContainer = container
	}
	if stage.installsPackages && auth != nil {
		stageContainer = auth.withExec(stageContainer, cmd)
	} else {
		stageContainer = stageContainer.WithExec(cmd)
	}

	stdout, err := stageContainer.Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout: %w", err)
	}
	stderr, err := stageContainer.Stderr(ctx)
	if err != nil {
		stderr = ""
	}

	if returnContainer == nil {
		returnContainer = stageContainer
	}
	artifactsDir := stageContainer.Directory(fmt.Sprintf("%s/%s", n.Workdir(), DefaultBuildDir))
	return &StageResult{
		Container: returnContainer,
		Stdout:    stdout,
		Stderr:    stderr,
		Artifacts: artifactsDir,
	}, nil
}

// registryAuthEnv é a variável que carrega a credencial dentro do container, só durante a instalação.
const registryAuthEnv = "NPM_REGISTRY_AUTH"

// registryAuthUserconfig é o userconfig temporário da instalação. Fica fora de DefaultWorkdir, para
// não cair em nada que seja copiado adiante, e não guarda segredo: só a referência à variável.
const registryAuthUserconfig = "/tmp/npm-registry-auth/npmrc"

// registryAuth é a credencial pronta para a instalação: o segredo e o userconfig que o referencia.
type registryAuth struct {
	secret     *dagger.Secret
	userconfig string
}

// registryAuth monta a credencial do registro a partir de RegistryUser, RegistryPassword e do
// .npmrc do projeto. Devolve nil quando não há credencial, ou quando o .npmrc não declara registro
// para ela.
func (n *Npm) registryAuth(ctx context.Context) (*registryAuth, error) {
	if n.RegistryPassword == nil {
		return nil, nil
	}
	if n.RegistryUser == "" {
		return nil, fmt.Errorf("credencial do registro npm: senha informada sem usuário")
	}
	npmrc, err := n.Source.File(n.npmrcPath()).Contents(ctx)
	if err != nil {
		npmrc = ""
	}
	registry := registryFromNpmrc(npmrc)
	if registry == "" {
		fmt.Printf("ℹ️  %s não declara `registry=`: npm ci sem credencial (o registro público não deve recebê-la).\n", n.npmrcPath())
		return nil, nil
	}
	keys, err := registryAuthKeys(registry)
	if err != nil {
		// Não derruba o build: o publish-all sempre repassa a credencial das imagens, e um .npmrc
		// que o npm entende mas este parser não (`registry=${VAR}`, por exemplo) seguia
		// funcionando sem ela até aqui.
		fmt.Printf("⚠️  registry=%q no %s: %v. npm ci sem credencial.\n", registry, n.npmrcPath(), err)
		return nil, nil
	}
	var lines []string
	for _, key := range keys {
		lines = append(lines,
			key+":_auth=${"+registryAuthEnv+"}",
			// O npm 6 (node <= 14) só manda a credencial para tarball em outro host com always-auth,
			// e lockfile v1 guarda a URL do tarball como foi resolvida -- às vezes por outro nome do
			// mesmo Nexus. O npm 7+ ignora a opção.
			key+":always-auth=true")
	}
	password, err := n.RegistryPassword.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("credencial do registro npm: ler a senha: %w", err)
	}
	// O _auth é um segredo novo, e não só texto: registrado no engine, ele também é mascarado na
	// saída, como a senha de que deriva.
	secret := dag.SetSecret("npm-registry-auth",
		base64.StdEncoding.EncodeToString([]byte(n.RegistryUser+":"+password)))
	fmt.Printf("🔐 Credencial do registro npm (usuário %s) aplicada a %s\n", n.RegistryUser, strings.Join(keys, ", "))
	return &registryAuth{secret: secret, userconfig: strings.Join(lines, "\n") + "\n"}, nil
}

// withExec roda cmd com a credencial e a retira do container em seguida: o userconfig, a variável
// que aponta para ele e a variável secreta só existem durante essa execução. Os passos seguintes
// (o build, o Sonar) e o diretório publicado não herdam nada.
func (a *registryAuth) withExec(container *dagger.Container, cmd []string) *dagger.Container {
	return container.
		WithNewFile(registryAuthUserconfig, a.userconfig).
		WithEnvVariable("NPM_CONFIG_USERCONFIG", registryAuthUserconfig).
		WithSecretVariable(registryAuthEnv, a.secret).
		WithExec(cmd).
		WithoutSecretVariable(registryAuthEnv).
		WithoutEnvVariable("NPM_CONFIG_USERCONFIG").
		WithoutFile(registryAuthUserconfig)
}

// npmrcPath é o caminho do .npmrc do projeto dentro de Source, ao lado do package.json.
func (n *Npm) npmrcPath() string {
	if n.ModulePath == "" {
		return ".npmrc"
	}
	return n.ModulePath + "/.npmrc"
}

// registryFromNpmrc devolve o valor da linha `registry=` de um .npmrc, ou "" se não houver.
// Registros por escopo (`@escopo:registry=`) não contam: a credencial é de um registro só.
func registryFromNpmrc(contents string) string {
	registry := ""
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "registry" {
			continue
		}
		// Como no npm, a última ocorrência vence.
		registry = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return registry
}

// registryAuthKeys devolve as chaves `//host/caminho/` sob as quais o npm procura a credencial do
// registro.
//
// O npm deriva a chave com url.resolve(registry, '.'), que descarta o último segmento quando a URL
// não termina em barra: `https://h/repository/npm-registry` vira `//h/repository/`. Se ele já
// acrescentou a barra antes depende da versão, então as duas formas entram.
func registryAuthKeys(registry string) ([]string, error) {
	u, err := url.Parse(registry)
	if err != nil {
		return nil, err
	}
	if u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("não é uma URL http(s) com host")
	}
	withSlash := u.Path
	if !strings.HasSuffix(withSlash, "/") {
		withSlash += "/"
	}
	resolved := u.ResolveReference(&url.URL{Path: "."}).Path
	if resolved == "" {
		resolved = "/"
	}
	keys := []string{"//" + u.Host + withSlash}
	if resolved != withSlash {
		keys = append(keys, "//"+u.Host+resolved)
	}
	return keys, nil
}

// buildSonarOptions converts the Sonar configuration into CLI arguments for the scanner.
//
// A montagem da lista fica em pipeline/sonarargs, compartilhada com os módulos
// maven e uv; aqui ficam apenas as bordas específicas do módulo: config nil vira
// (nil, nil) e o token é lido do Secret.
func (n *Npm) buildSonarOptions(ctx context.Context, config *SonarConfig) ([]string, error) {
	// TODO see: sonar.projectName and sonar.projectVersion
	if config == nil {
		return nil, nil
	}

	token, err := config.TokenSecret.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get sonar token: %w", err)
	}

	return sonarargs.BuildOptions(
		config.Host,
		token,
		config.ProjectKey,
		config.WaitForQualityGate,
		config.ExtraOptions,
	), nil
}

// withPackageJsonVersion reads package.json, replaces the version field and returns the updated source directory.
func (n *Npm) withPackageJsonVersion(ctx context.Context, version string) (*dagger.Directory, error) {
	contents, err := n.Source.File(n.packageJSONPath()).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contents), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}
	versionJSON, _ := json.Marshal(version)
	raw["version"] = versionJSON
	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize package.json: %w", err)
	}
	return n.Source.WithNewFile(n.packageJSONPath(), string(updated)+"\n"), nil
}

func (n *Npm) GetVersion(ctx context.Context) (string, error) {
	if n.Source == nil {
		return "", fmt.Errorf("cannot get NPM version: npm directory is not set")
	}
	pkgJSON, err := n.Source.File(n.packageJSONPath()).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to read package.json from directory: %w", err)
	}
	var pkg packageJSON
	if err := json.Unmarshal([]byte(pkgJSON), &pkg); err != nil {
		return "", fmt.Errorf("failed to parse package.json: %w", err)
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("could not find 'version' key in package.json")
	}
	return pkg.Version, nil
}

func (n *Npm) GetVersionOrDefault(ctx context.Context, defaultVersion string) string {
	version, err := n.GetVersion(ctx)
	if err != nil {
		version = defaultVersion
	}
	return version
}

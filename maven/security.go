package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"fmt"
)

// DependencyCheckReportGlob casa os relatórios do OWASP Dependency-Check em qualquer módulo
// da árvore montada. O plugin escreve em ${project.build.directory}, e num reactor com -am
// cada módulo do build tem o seu -- o do módulo-alvo e os das libs irmãs.
const DependencyCheckReportGlob = "**/target/dependency-check-report.*"

// DependencyCheckJUnitGlob casa o relatório JUnit do Dependency-Check (formato JUNIT), que não
// segue o nome dos outros: o plugin o grava como dependency-check-junit.xml.
const DependencyCheckJUnitGlob = "**/target/dependency-check-junit.xml"

// securityReportIncludes são os filtros aplicados à árvore do build para recolher os relatórios.
// O "**/" casa um ou mais diretórios, então o target/ da raiz montada entra à parte.
func securityReportIncludes() []string {
	return []string{
		DependencyCheckReportGlob, "target/dependency-check-report.*",
		DependencyCheckJUnitGlob, "target/dependency-check-junit.xml",
	}
}

// SecurityDataPath é onde a base NVD pronta é montada no container do scan.
const SecurityDataPath = "/odc-data"

// securityDataOptions são as properties que apontam o plugin para a base pronta e desligam a
// atualização. Os nomes são os parâmetros `dataDirectory` e `autoUpdate` do dependency-check-maven
// 13.0.0, lidos do plugin.xml do jar (a user property tem o mesmo nome do parâmetro). Vão depois
// das ExtraOptions do módulo, então valem sobre um -D igual vindo do pipeline.toml; configuração
// explícita no pom ainda vence a linha de comando.
func securityDataOptions(withData bool) []string {
	if !withData {
		return nil
	}
	return []string{"-DdataDirectory=" + SecurityDataPath, "-DautoUpdate=false"}
}

// SecurityScanResult é o desfecho de uma varredura: os relatórios e o código de saída do mvn.
type SecurityScanResult struct {
	// Relatórios do Dependency-Check, com o caminho relativo à árvore montada preservado
	// (ex: `target/dependency-check-report.html`, `target/dependency-check-junit.xml`, ou `<módulo>/target/...` num reactor).
	Reports *dagger.Directory
	// Código de saída do `mvn verify`. Zero é sucesso.
	ExitCode int
}

// SecurityScan roda `mvn clean verify` no módulo e devolve os relatórios mesmo quando o build falha.
//
// É o que FullBuild não consegue fazer: lá um exit code diferente de zero vira erro do WithExec e
// o container -- com o target/ dentro -- é descartado junto. Numa varredura de segurança o build
// que falhou é exatamente o que tem relatório a mostrar (failBuildOnCVSS estourado), então o exec
// roda com Expect=ANY e o código de saída volta como dado, para quem chama decidir o que é falha.
//
// Quem liga o plugin são as ExtraOptions do módulo (o perfil do pom e o formato do relatório);
// aqui não se sabe nada do Dependency-Check além de onde o relatório cai.
func (m *Maven) SecurityScan(ctx context.Context,
	source *dagger.Directory,
	// Module name. In reactor mode, the module path relative to the reactor root.
	module string,
	// Caminho do módulo relativo à raiz do repositório; mesma semântica do modulePath de FullBuild.
	// +optional
	modulePath string,
	// Base NVD do Dependency-Check já pronta (ver UpdateSecurityData e o orchestrator-utils
	// SecurityData). Com ela, o diretório é montado em /odc-data -- fora do cache volume -- e o
	// plugin roda com -DdataDirectory=/odc-data -DautoUpdate=false: o scan não fala com a API da
	// NVD. Sem ela, o plugin mantém o comportamento de sempre e atualiza a base sozinho.
	// +optional
	securityData *dagger.Directory,
) (*SecurityScanResult, error) {
	moduleDir := module
	rootMounted := m.ReactorMode
	if !m.ReactorMode && modulePath != "" {
		moduleDir = modulePath
		rootMounted = true
	}
	var options []string
	if m.ReactorMode {
		options = reactorOptions(module, "", "-am")
	}
	options = append(options, securityDataOptions(securityData != nil)...)
	ctr := m.mountSource(source, moduleDir, rootMounted)
	if securityData != nil {
		ctr = ctr.WithDirectory(SecurityDataPath, securityData)
	}
	ctr = ctr.
		WithExec(m.getFullMvnModuleCommand(options, []string{"clean", "verify"}),
			dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	exitCode, err := ctr.ExitCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("mvn verify: %w", err)
	}
	// Montada só a árvore do módulo, o relatório fica em /app/<módulo>/target; montada a raiz,
	// em /app/<qualquer módulo>/target. Filtrar a árvore inteira cobre os dois casos.
	root := BaseWorkdir
	if !rootMounted {
		root = fmt.Sprintf("%s/%s", BaseWorkdir, moduleDir)
	}
	reports := dag.Directory().WithDirectory("/", ctr.Directory(root),
		dagger.DirectoryWithDirectoryOpts{Include: securityReportIncludes()})
	return &SecurityScanResult{Reports: reports, ExitCode: exitCode}, nil
}

// DefaultOdcVersion é a versão do dependency-check-maven que o aquecimento roda. O esquema do H2
// muda entre majors, por isso a tag da imagem de dados é a major.
const DefaultOdcVersion = "13.0.0"

// updateSecurityDataCommand monta o `mvn ...:update-only`. O goal não exige projeto
// (requiresProject=false no plugin.xml do 13.0.0), então roda num diretório sem pom.
//
// Retries e espera maiores que os do padrão: aqui um 503 da NVD derruba o aquecimento inteiro, e
// ele não tem o timeout apertado de um scan. A chave vai pelo ambiente (nvdApiKeyEnvironmentVariable),
// nunca como -D, para não aparecer na linha de comando do processo.
func updateSecurityDataCommand(odcVersion string) []string {
	return []string{
		"mvn", "--batch-mode", "--errors",
		"org.owasp:dependency-check-maven:" + odcVersion + ":update-only",
		"-DdataDirectory=" + SecurityDataPath,
		"-DnvdApiKeyEnvironmentVariable=NVD_API_KEY",
		"-DnvdMaxRetryCount=60",
		"-DnvdApiDelay=2000",
		"-DretireJsForceUpdate=true",
	}
}

// UpdateSecurityData atualiza a base NVD do Dependency-Check e devolve o diretório de dados.
//
// Com `previous` (a publicação anterior) a atualização é incremental e leva minutos; só o
// bootstrap, sem base, paga as horas do download completo. Atualiza também RetireJS, CISA KEV e
// supressões hospedadas (update-only roda todos os data sources), porque os scans consumidores
// rodam com autoUpdate=false e não buscariam nada disso.
//
// +cache="never"
func (m *Maven) UpdateSecurityData(ctx context.Context,
	// Chave da API da NVD. Só entra como variável secreta do container; não é gravada na base.
	nvdApiKey *dagger.Secret,
	// Versão do dependency-check-maven.
	// +default="13.0.0"
	odcVersion string,
	// Base publicada antes, para a atualização incremental. Sem ela é o bootstrap.
	// +optional
	previous *dagger.Directory,
) (*dagger.Directory, error) {
	if nvdApiKey == nil {
		return nil, fmt.Errorf("UpdateSecurityData precisa da chave da API da NVD")
	}
	if odcVersion == "" {
		odcVersion = DefaultOdcVersion
	}
	data := previous
	if data == nil {
		data = dag.Directory()
	}
	ctr := m.NewBaseContainer().
		WithSecretVariable("NVD_API_KEY", nvdApiKey).
		WithDirectory(SecurityDataPath, data).
		WithWorkdir("/tmp/odc-update").
		WithExec(updateSecurityDataCommand(odcVersion))
	return ctr.Directory(SecurityDataPath), nil
}

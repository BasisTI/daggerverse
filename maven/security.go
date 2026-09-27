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
	ctr := m.mountSource(source, moduleDir, rootMounted).
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

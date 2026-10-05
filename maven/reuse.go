package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"strings"
)

// AnalysisChecksumFile é o arquivo, na raiz do diretório de AnalysisInputs, com o sha256 de cada
// arquivo exportado. Quem restaura o diretório o tira antes.
const AnalysisChecksumFile = "sonar-reuse.sha256"

// analysisFilterScript escolhe, na árvore de um módulo, o que vai para a análise: dentro de qualquer
// `target/` (do módulo e de cada filho), SÓ o que o scanner lê, reconhecido pelo CONTEÚDO e não pelo
// nome ou pela extensão.
//
//   - `*.class`: o bytecode (sonar.java.binaries e sonar.java.test.binaries);
//   - XML cujo elemento raiz é `testsuite` ou `testsuites`: relatório de teste, esteja onde o pom
//     mandou o surefire/failsafe gravar;
//   - XML cujo raiz é `report` e que declara o DOCTYPE do JaCoCo, e `jacoco*.exec`: cobertura;
//   - `*.java` e `*.kt` sob `generated-sources` e `generated-test-sources`.
//
// Nada mais viaja. Tentamos antes uma lista de exclusão por extensão, e ela vazou recursos filtrados
// com segredo (`generated-resources/auth.xml`, `.env.production`, `token.json`, `settings.xml`):
// qualquer coisa que o pom mande gravar em target/ é candidata, e não se enumera o que pode conter
// segredo. Por conteúdo, um `auth.xml` tem raiz `auth` e fica de fora, qualquer que seja o nome ou o
// diretório. O que os relatórios de teste trazem dentro (system-out, propriedades da JVM) continua
// viajando: o scanner os lê.
//
// Em seguida grava, em AnalysisChecksumFile, o sha256 do que foi exportado, que AnalysisInputsProblem
// confere do outro lado. Só POSIX sh, find, grep, head, tr, tar e sha256sum, que as imagens do Maven
// (Debian, Ubuntu e Alpine) trazem. IN e OUT vêm do ambiente para o script poder ser testado fora de
// container.
const analysisFilterScript = `set -eu
IN="${IN:-/in}"; OUT="${OUT:-/out}"; KEEP="$(mktemp)"
cd "$IN"
mkdir -p "$OUT"
: > "$KEEP"
find . -name node_modules -prune -o \( -type f -path '*/target/*' \( -name '*.class' -o -name 'jacoco*.exec' \) -print \) >> "$KEEP"
find . -name node_modules -prune -o \( -type f -path '*/target/*' \( -name '*.java' -o -name '*.kt' \) \
  \( -path '*/generated-sources/*' -o -path '*/generated-test-sources/*' \) -print \) >> "$KEEP"
find . -name node_modules -prune -o \( -type f -path '*/target/*' -name '*.xml' -print \) | while IFS= read -r f; do
  head="$(head -c 1024 "$f" | tr -d '\000')"
  root="$(printf '%s' "$head" | grep -o '<[A-Za-z][A-Za-z0-9_:.-]*' | head -n 1 || true)"
  case "$root" in
    '<testsuite'|'<testsuites') printf '%s\n' "$f" ;;
    '<report') case "$head" in *'-//JACOCO//DTD Report'*) printf '%s\n' "$f" ;; esac ;;
  esac
done >> "$KEEP"
if [ -s "$KEEP" ]; then
  tar -cf - -T "$KEEP" | tar -xf - -C "$OUT"
  cd "$OUT"
  SUMS="$(mktemp)"
  find . -type f -exec sha256sum {} + > "$SUMS"
  cp "$SUMS" "` + AnalysisChecksumFile + `"
fi
`

// analysisVerifyOK é a última linha que analysisVerifyScript imprime quando a verificação terminou.
// Sem ela, a verificação não terminou, e o resultado é um problema, nunca um "está tudo bem".
const analysisVerifyOK = "VERIFICACAO-CONCLUIDA"

// analysisVerifyScript confere o diretório recebido contra o AnalysisChecksumFile: arquivo alterado,
// truncado ou perdido, e relatório XML vazio. Imprime o que está errado e, ao fim, analysisVerifyOK.
//
// Falha fechado: antes de começar confere que as ferramentas existem, e só imprime o marcador final
// se chegou ao fim. Um `grep` ausente já fez a verificação inteira passar em branco (o erro ia para o
// stderr e o stdout ficava vazio); agora a ausência de ferramenta, ou qualquer saída antes da hora,
// vira um motivo explícito e o target roda o check completo.
const analysisVerifyScript = `IN="${IN:-/in}"
for tool in sha256sum find grep head sed; do
  command -v "$tool" >/dev/null 2>&1 || { echo "ferramenta ausente na imagem: $tool"; exit 0; }
done
cd "$IN" || { echo "diretório de relatórios inacessível"; exit 0; }
if [ ! -f ` + AnalysisChecksumFile + ` ]; then echo "sem ` + AnalysisChecksumFile + `"; exit 0; fi
sums="$(sha256sum -c ` + AnalysisChecksumFile + ` 2>&1)"; rc=$?
if [ "$rc" -ne 0 ]; then
  bad="$(printf '%s\n' "$sums" | grep -v ': OK$' | head -n 20)"
  [ -n "$bad" ] || bad="sha256sum terminou com código $rc sem detalhar"
  printf '%s\n' "$bad"
fi
find . -type f -name '*.xml' -size 0 | head -n 20 | sed 's/$/: relatório XML vazio/'
echo ` + analysisVerifyOK + `
`

// AnalysisInputs recorta de um módulo construído o que a análise do Sonar precisa.
//
// Recebe a `Tree` do resultado de FullBuild -- a árvore do módulo depois do build, com o target/
// dele e o de cada módulo filho -- e devolve um diretório com os mesmos caminhos relativos ao
// módulo, mais o AnalysisChecksumFile, pronto para voltar à árvore por AnalyzeFromReports. O que
// entra é decidido pelo conteúdo: ver analysisFilterScript.
func (m *Maven) AnalysisInputs(
	// A árvore do módulo, como devolvida em `tree` por FullBuild.
	tree *dagger.Directory,
) *dagger.Directory {
	return dag.Container().From(m.Image).
		WithMountedDirectory("/in", tree).
		WithNewFile("/filter.sh", analysisFilterScript).
		WithExec([]string{"sh", "/filter.sh"}).
		Directory("/out")
}

// AnalysisInputsProblem confere um diretório de AnalysisInputs contra o checksum que ele traz e
// devolve o que está errado, ou texto vazio quando está íntegro. Pega o arquivo truncado, vazio ou
// trocado que a contagem do manifesto não vê.
//
// O checksum viaja no mesmo artefato: protege contra perda e corrupção, não contra quem reescreve o
// artefato (ver o README).
func (m *Maven) AnalysisInputsProblem(ctx context.Context,
	// O diretório de AnalysisInputs, como chegou à análise.
	inputs *dagger.Directory,
) (string, error) {
	out, err := dag.Container().From(m.Image).
		WithMountedDirectory("/in", inputs).
		WithNewFile("/verify.sh", analysisVerifyScript).
		WithExec([]string{"sh", "/verify.sh"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	// Sem o marcador final a verificação não terminou (ferramenta ausente, saída antecipada): é um
	// problema, e o motivo é o que o script disse, ou a falta dele.
	out = strings.TrimSpace(out)
	rest, finished := strings.CutSuffix(out, analysisVerifyOK)
	rest = strings.TrimSpace(rest)
	if !finished {
		if rest == "" {
			rest = "a verificação não terminou"
		}
		return rest, nil
	}
	return rest, nil
}

// reusePrepOptions são as opções do estágio que recoloca no repositório local as dependências
// irmãs de um reactor, quando a análise roda num engine que não tem o cache do build.
//
// O `install` roda sem testes e sem o Dependency-Check: o que se quer é só o artefato dos
// módulos irmãos em ~/.m2, para o `-f <módulo>/pom.xml` do Sonar resolver o classpath. O
// maven-install-plugin do pom-esqueleto do reactor está amarrado à fase verify, por isso o
// goal é `install` e não `package`.
func reusePrepOptions(module string) []string {
	return reactorOptions(module, "", "-am",
		"-DskipTests", "-Dmaven.test.skip=true", "-DskipITs",
		"-Ddependency-check.skip=true", "-Dodc.skip=true")
}

// AnalyzeFromReports roda só a análise do Sonar de um módulo, sobre o resultado de um build já feito.
//
// É o par de FullBuild para o job de análise de branch: o `verify` já rodou no job de publish, e
// repeti-lo custa o tempo de todos os testes. Aqui os arquivos de AnalysisInputs voltam à árvore do
// módulo e o Maven roda o `sonar:sonar` -- que resolve o classpath das bibliotecas sozinho
// (o goal exige resolução de dependências) e não recompila nada.
//
// Quem chama é responsável por só passar inputs do mesmo commit e completos: esta função não os
// confere. A conferência é do orchestrator, com o manifesto que o publish grava.
//
// Num build comum o `sonar:sonar` já era uma segunda invocação do Maven sobre o target/ deixado
// pela primeira; o que muda é de onde vem esse target/, não o que o scanner enxerga.
//
// Em reactor, as dependências irmãs precisam estar no ~/.m2: um estágio de `install` sem testes
// as garante, e custa pouco quando o cache do build está no mesmo engine.
func (m *Maven) AnalyzeFromReports(ctx context.Context,
	source *dagger.Directory,
	// Module name. In reactor mode, the module path relative to the reactor root.
	module string,
	// O que AnalysisInputs recortou da árvore do módulo no build.
	reports *dagger.Directory,
	sonarConfig *SonarConfig,
	// Caminho do módulo relativo à raiz do repositório; mesma semântica do modulePath de FullBuild.
	// +optional
	modulePath string,
) (*ModuleBuildResult, error) {
	// Nenhum teste roda aqui: o daemon do Testcontainers custaria um dind sem ninguém para usá-lo.
	m.UseDocker = false

	moduleDir := module
	rootMounted := m.ReactorMode
	if !m.ReactorMode && modulePath != "" {
		moduleDir = modulePath
		rootMounted = true
	}

	var stages []PipelineStage
	if m.ReactorMode {
		stages = append(stages, PipelineStage{
			DisplayName: "Resolve sibling modules",
			Goals:       []string{"install"},
			Options:     reusePrepOptions(module),
		})
	}
	sonarStage, err := m.configureSonar(ctx, sonarConfig, module)
	if err != nil {
		return nil, err
	}
	if m.ReactorMode {
		sonarStage.Options = append(sonarReactorOptions(module, ""), sonarStage.Options...)
	}
	stages = append(stages, sonarStage)

	return m.executeStages(ctx, source, module, moduleDir, rootMounted, reports, stages, nil, "")
}

package main

import (
	"context"
	"dagger/orchestrator-utils/internal/dagger"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// maxBumpAttempts é o limite de tentativas do bump quando o push é recusado por corrida (a branch
// andou ou o ref foi travado por outro push). Pequeno de propósito: cada rodada refaz o fetch e uma branch que
// anda mais de 4 vezes em segundos indica outro problema, que deve falhar o job.
const maxBumpAttempts = 4

// parseCalVer separa uma versão CalVer (AAAA.MM.DD.IID) em partes numéricas. Devolve ok=false
// quando alguma parte está vazia, não é numérica ou não cabe em uint64.
func parseCalVer(v string) ([]uint64, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]uint64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// calVerGreater informa se a versão candidate é estritamente maior que current, comparando
// as partes numericamente (2026.11.17.9 < 2026.11.17.10). Parte ausente vale 0. O segundo
// retorno é false quando alguma das duas versões não é interpretável.
func calVerGreater(candidate, current string) (greater, comparable bool) {
	a, ok := parseCalVer(candidate)
	if !ok {
		return false, false
	}
	b, ok := parseCalVer(current)
	if !ok {
		return false, false
	}
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y uint64
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y, true
		}
	}
	return false, true
}

// currentFileVersion lê a versão atual do arquivo, com as mesmas regras de localização do bump.
// Devolve ok=false quando a versão não está lá ou o arquivo não é interpretável: nesse caso o
// bump grava, como sempre fez.
func currentFileVersion(content, filename, versionType string) (string, bool) {
	if versionType == "plain" {
		return strings.TrimSpace(content), true
	}
	switch filename {
	case "pom.xml":
		v, found, err := findPomElement(content, "project", "version")
		if err != nil || !found {
			return "", false
		}
		if strings.TrimSpace(v) != revisionPlaceholder {
			return strings.TrimSpace(v), true
		}
		r, found, err := findPomElement(content, "project", "properties", "revision")
		if err != nil || !found {
			return "", false
		}
		return strings.TrimSpace(r), true
	case "package.json":
		var raw struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(content), &raw); err != nil || raw.Version == "" {
			return "", false
		}
		return raw.Version, true
	case "pyproject.toml":
		var cfg pyProject
		if err := toml.Unmarshal([]byte(content), &cfg); err != nil || cfg.Project.Version == "" {
			return "", false
		}
		return cfg.Project.Version, true
	}
	return "", false
}

// planFileBump decide se o arquivo deve ser gravado e com qual conteúdo (guarda monotônica):
// só grava quando a versão nova é maior que a do arquivo. Versão do arquivo ou da pipeline
// não interpretável ⇒ grava. Conteúdo final idêntico ao atual ⇒ não grava.
func planFileBump(content, version, filename, versionType string) (modified string, write bool, err error) {
	if cur, ok := currentFileVersion(content, filename, versionType); ok {
		if greater, comparable := calVerGreater(version, cur); comparable && !greater {
			return content, false, nil
		}
	}
	modified, err = bumpFileVersion(content, version, filename, versionType)
	if err != nil {
		return "", false, err
	}
	return modified, modified != content, nil
}

// fileChange é um arquivo de versão com o conteúdo novo a gravar.
type fileChange struct {
	Path    string
	Content string
}

// bumpAttempt é o checkout do head remoto de uma tentativa.
type bumpAttempt interface {
	// Read devolve o conteúdo do arquivo no head buscado.
	Read(ctx context.Context, path string) (string, error)
	// CommitAndPush grava os arquivos sobre o head buscado, commita e empurra com ci.skip.
	CommitAndPush(ctx context.Context, changes []fileChange, message string) error
}

// bumpFetcher busca o head atual da branch remota. Cada chamada deve refazer o fetch de verdade.
type bumpFetcher func(ctx context.Context) (bumpAttempt, error)

// runBump reaplica o bump sobre o head remoto, repetindo desde o fetch quando o push é
// recusado por corrida (ver motivoRepetivel). Outras falhas de push falham de imediato.
func runBump(ctx context.Context, fetch bumpFetcher, versionFiles, versionFileTypes []string,
	version, message string, attempts int) error {
	var lastErr error
	for n := 1; n <= attempts; n++ {
		at, err := fetch(ctx)
		if err != nil {
			return fmt.Errorf("falha ao buscar o head remoto: %w", err)
		}

		var changes []fileChange
		for i, vf := range versionFiles {
			content, err := at.Read(ctx, vf)
			if err != nil {
				if arquivoAusente(err) {
					return fmt.Errorf("arquivo de versão %s não encontrado no head remoto (removido ou renomeado depois do início da pipeline?): %w", vf, err)
				}
				return fmt.Errorf("failed to read %s: %w", vf, err)
			}
			modified, write, err := planFileBump(content, version, baseName(vf), versionFileTypes[i])
			if err != nil {
				return err
			}
			if !write {
				fmt.Printf("BumpAndCommitVersions: %s já está em versão igual ou maior que %s — pulando\n", vf, version)
				continue
			}
			changes = append(changes, fileChange{Path: vf, Content: modified})
			fmt.Printf("BumpAndCommitVersions: %s -> %s\n", vf, version)
		}
		if len(changes) == 0 {
			fmt.Println("BumpAndCommitVersions: nenhum arquivo precisa de bump, sem commit")
			return nil
		}

		err = at.CommitAndPush(ctx, changes, message)
		if err == nil {
			return nil
		}
		if !isPushRejected(err) {
			return fmt.Errorf("commit-and-push failed: %w", err)
		}
		lastErr = err
		fmt.Printf("BumpAndCommitVersions: push recusado por corrida (tentativa %d/%d); refazendo a partir do fetch\n", n, attempts)
	}
	return fmt.Errorf("commit-and-push failed: push recusado após %d tentativas: %w", attempts, lastErr)
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// motivoRecusaPush captura o motivo entre parênteses de uma linha de recusa do git:
//
//	! [rejected]        HEAD -> develop (non-fast-forward)
//	! [remote rejected] HEAD -> develop (incorrect old value provided)
//
// A decisão é pelo motivo, não pelo prefixo "[remote rejected]", que também aparece na recusa por
// hook, branch protegida e permissão. Linhas "remote:" (texto livre de hook) são ignoradas.
var motivoRecusaPush = regexp.MustCompile(`(?m)^\s*!\s+\[(?:remote rejected|rejected)\]\s+\S+\s+->\s+\S+\s+\((.+)\)\s*$`)

// motivoRepetivel informa se o motivo de uma recusa é corrida (a branch andou ou o ref está
// travado por outro push):
//   - non-fast-forward, fetch first: recusa no cliente, a branch avançou.
//   - incorrect old value provided: receive-pack, o ref mudou entre a leitura e a atualização.
//   - reference already exists: git 2.x, lock do ref ocupado por outro push.
//   - cannot lock ref ...: git antigo, lock do ref ocupado.
//   - failed to update ref (singular): git antigo, atualização do ref perdida. O plural
//     "failed to update refs" é o que o git atual diz para falha permanente (permissão negada,
//     disco cheio) e não repete.
func motivoRepetivel(motivo string) bool {
	motivo = strings.TrimSpace(motivo)
	switch motivo {
	case "non-fast-forward", "fetch first", "incorrect old value provided",
		"reference already exists", "failed to update ref":
		return true
	}
	return strings.HasPrefix(motivo, "cannot lock ref")
}

// stderrDoExec devolve o Stderr de um *dagger.ExecError na cadeia do erro, sem chamar Error() dele.
func stderrDoExec(err error) string {
	var ee *dagger.ExecError
	if errors.As(err, &ee) {
		return ee.Stderr
	}
	return ""
}

// isPushRejected reconhece a recusa de push por corrida. Recusa por hook (pre-receive hook
// declined), branch protegida, credencial e permissão não casam e falham de imediato.
func isPushRejected(err error) bool {
	text := err.Error() + "\n" + stderrDoExec(err)
	for _, m := range motivoRecusaPush.FindAllStringSubmatch(text, -1) {
		if motivoRepetivel(m[1]) {
			return true
		}
	}
	// Dica do cliente quando o remoto tem trabalho que não temos localmente.
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "hint:") && strings.Contains(l, "remote contains work") {
			return true
		}
	}
	return false
}

// arquivoAusente informa se o erro de leitura é de arquivo inexistente (local ou do engine).
func arquivoAusente(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "no such file or directory")
}

// daggerBumpAttempt implementa bumpAttempt sobre um container git com o head remoto em /src.
type daggerBumpAttempt struct {
	git       *dagger.Container
	branch    string
	remoteUrl string
}

// newDaggerBumpFetcher monta o fetcher real: a cada chamada, um container novo com
// CACHE_BUSTER (o Dagger cacheia execs; sem ele o fetch seria reaproveitado), fetch da branch e
// checkout do FETCH_HEAD sobre o repositório da pipeline.
func newDaggerBumpFetcher(source *dagger.Directory, branch, remoteUrl string) bumpFetcher {
	return func(ctx context.Context) (bumpAttempt, error) {
		git := dag.Container().From("alpine/git").
			WithEnvVariable("GIT_SSL_CAINFO", "/etc/ssl/certs/ca-certificates.crt").
			WithEnvVariable("CACHE_BUSTER", time.Now().Format(time.RFC3339Nano)).
			WithDirectory("/src", source).
			WithWorkdir("/src").
			WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "/src"}).
			WithExec([]string{"git", "config", "user.email", "ci-bot@basis.com.br"}).
			WithExec([]string{"git", "config", "user.name", "CI Bot"}).
			WithExec([]string{"git", "fetch", remoteUrl, branch}).
			WithExec([]string{"git", "checkout", "-f", "--detach", "FETCH_HEAD"})
		if _, err := git.Sync(ctx); err != nil {
			return nil, err
		}
		return &daggerBumpAttempt{git: git, branch: branch, remoteUrl: remoteUrl}, nil
	}
}

func (a *daggerBumpAttempt) Read(ctx context.Context, path string) (string, error) {
	return a.git.File("/src/" + path).Contents(ctx)
}

func bumpPushArgs(remoteUrl, branch string) []string {
	return []string{"git", "push", "-o", "ci.skip", remoteUrl, "HEAD:" + branch}
}

func (a *daggerBumpAttempt) CommitAndPush(ctx context.Context, changes []fileChange, message string) error {
	git := a.git
	for _, c := range changes {
		git = git.WithNewFile("/src/"+c.Path, c.Content).WithExec([]string{"git", "add", c.Path})
	}
	// O push depende do CACHE_BUSTER do fetch desta tentativa, então o Dagger não o reaproveita.
	_, err := git.
		WithExec([]string{"git", "commit", "-m", message}).
		WithExec(bumpPushArgs(a.remoteUrl, a.branch)).
		Sync(ctx)
	return err
}

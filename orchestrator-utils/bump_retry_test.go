package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCalVerGreater(t *testing.T) {
	casos := []struct {
		novo, atual string
		maior, comp bool
	}{
		{"2026.11.17.10", "2026.11.17.9", true, true}, // numérico, não lexicográfico
		{"2026.11.17.9", "2026.11.17.10", false, true},
		{"2026.11.17.5", "2026.11.17.5", false, true}, // igual não é maior
		{"2026.11.18.1", "2026.11.17.99", true, true},
		{"2027.01.01.1", "2026.12.31.99", true, true},
		{"2026.11.17.1", "2026.11.17", true, true}, // parte ausente vale 0
		{"2026.11.17", "2026.11.17.0", false, true},
		{"2026.11.17.1", "1.0.0-SNAPSHOT", false, false},
		{"2026.11.17.1", "", false, false},
		{"abc", "2026.11.17.1", false, false},
		{"2026.11.17.1", "99999999999999999999.1", false, false},
	}
	for _, c := range casos {
		g, ok := calVerGreater(c.novo, c.atual)
		if g != c.maior || ok != c.comp {
			t.Errorf("calVerGreater(%q, %q) = (%v, %v), quer (%v, %v)", c.novo, c.atual, g, ok, c.maior, c.comp)
		}
	}
}

const pkgJSON = "{\n  \"name\": \"x\",\n  \"version\": \"2026.11.17.5\"\n}\n"
const pyproj = "[project]\nname = \"x\"\nversion = \"2026.11.17.5\"\n"

func TestPlanFileBump(t *testing.T) {
	casos := []struct {
		nome, content, file, tipo, versao string
		grava                             bool
		contem                            string
	}{
		{"pom maior", plainPom, "pom.xml", "", "2026.11.17.7", true, "<version>2026.11.17.7</version>"},
		{"pom igual", strings.ReplaceAll(plainPom, "2026.01.01.1", "2026.11.17.7"), "pom.xml", "", "2026.11.17.7", false, ""},
		{"pom menor", strings.ReplaceAll(plainPom, "2026.01.01.1", "2026.11.17.9"), "pom.xml", "", "2026.11.17.7", false, ""},
		{"reactor maior", revisionPom, "pom.xml", "", "2026.11.17.7", true, "<revision>2026.11.17.7</revision>"},
		{"reactor menor", strings.ReplaceAll(revisionPom, "2026.01.01.1", "2026.12.01.1"), "pom.xml", "", "2026.11.17.7", false, ""},
		{"package maior", pkgJSON, "package.json", "", "2026.11.17.6", true, `"version": "2026.11.17.6"`},
		{"package menor", pkgJSON, "package.json", "", "2026.11.17.4", false, ""},
		{"pyproject maior", pyproj, "pyproject.toml", "", "2026.11.18.1", true, `version = "2026.11.18.1"`},
		{"pyproject igual", pyproj, "pyproject.toml", "", "2026.11.17.5", false, ""},
		{"plain maior", "2026.11.17.5\n", "VERSION", "plain", "2026.11.17.6", true, "2026.11.17.6\n"},
		{"plain menor", "2026.11.17.5\n", "VERSION", "plain", "2026.11.17.1", false, ""},
		{"plain não interpretável grava", "dev\n", "VERSION", "plain", "2026.11.17.1", true, "2026.11.17.1\n"},
		{"pom snapshot grava", strings.ReplaceAll(plainPom, "2026.01.01.1", "1.0-SNAPSHOT"), "pom.xml", "", "2026.11.17.1", true, "2026.11.17.1"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			mod, grava, err := planFileBump(c.content, c.versao, c.file, c.tipo)
			if err != nil {
				t.Fatal(err)
			}
			if grava != c.grava {
				t.Fatalf("grava = %v, quer %v", grava, c.grava)
			}
			if grava && !strings.Contains(mod, c.contem) {
				t.Errorf("conteúdo sem %q:\n%s", c.contem, mod)
			}
		})
	}
}

func TestPlanFileBumpPomSemVersaoContinuaErro(t *testing.T) {
	if _, _, err := planFileBump("<project></project>", "2026.11.17.1", "pom.xml", ""); err == nil {
		t.Fatal("esperava erro")
	}
}

// --- laço de nova tentativa, com repositório git local e remoto bare ---

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// localAttempt imita daggerBumpAttempt com git local: mesmo fluxo fetch → checkout FETCH_HEAD →
// commit → push -o ci.skip HEAD:branch.
type localAttempt struct {
	t       *testing.T
	dir     string
	remote  string
	branch  string
	pushErr error
}

func (a *localAttempt) Read(_ context.Context, p string) (string, error) {
	b, err := os.ReadFile(filepath.Join(a.dir, p))
	return string(b), err
}

func (a *localAttempt) CommitAndPush(_ context.Context, changes []fileChange, message string) error {
	for _, c := range changes {
		if err := os.WriteFile(filepath.Join(a.dir, c.Path), []byte(c.Content), 0o644); err != nil {
			return err
		}
		git(a.t, a.dir, "add", c.Path)
	}
	git(a.t, a.dir, "commit", "-m", message)
	cmd := exec.Command("git", bumpPushArgs(a.remote, a.branch)[1:]...)
	cmd.Dir = a.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

type repoFixture struct {
	t      *testing.T
	bare   string
	work   string // checkout "da pipeline", atrás do remoto
	other  string // checkout que avança a develop
	fetchs int
}

func newFixture(t *testing.T) *repoFixture {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	root := t.TempDir()
	f := &repoFixture{t: t, bare: filepath.Join(root, "remote.git"), work: filepath.Join(root, "work"), other: filepath.Join(root, "other")}
	git(t, root, "init", "--bare", "-b", "develop", f.bare)
	git(t, f.bare, "config", "receive.advertisePushOptions", "true")
	git(t, root, "clone", f.bare, f.work)
	git(t, f.work, "checkout", "-b", "develop")
	os.WriteFile(filepath.Join(f.work, "VERSION"), []byte("2026.11.17.1\n"), 0o644)
	os.WriteFile(filepath.Join(f.work, "app.txt"), []byte("a\n"), 0o644)
	git(t, f.work, "add", ".")
	git(t, f.work, "commit", "-m", "inicial")
	git(t, f.work, "push", "origin", "develop")
	git(t, root, "clone", "-b", "develop", f.bare, f.other)
	return f
}

// avanca simula outra MR entrando na develop.
func (f *repoFixture) avanca(versao string) {
	if versao != "" {
		os.WriteFile(filepath.Join(f.other, "VERSION"), []byte(versao+"\n"), 0o644)
	} else {
		os.WriteFile(filepath.Join(f.other, "app.txt"), []byte(fmt.Sprintf("a%d\n", f.fetchs)), 0o644)
	}
	git(f.t, f.other, "add", ".")
	git(f.t, f.other, "commit", "-m", "outra MR")
	git(f.t, f.other, "pull", "--rebase", "origin", "develop")
	git(f.t, f.other, "push", "origin", "develop")
}

// fetcher refaz o fetch a cada chamada; depoisDoFetch roda entre o fetch e o push.
func (f *repoFixture) fetcher(depoisDoFetch func(n int)) bumpFetcher {
	return func(ctx context.Context) (bumpAttempt, error) {
		f.fetchs++
		git(f.t, f.work, "fetch", f.bare, "develop")
		git(f.t, f.work, "checkout", "-f", "--detach", "FETCH_HEAD")
		if depoisDoFetch != nil {
			depoisDoFetch(f.fetchs)
		}
		return &localAttempt{t: f.t, dir: f.work, remote: f.bare, branch: "develop"}, nil
	}
}

func (f *repoFixture) remoteVersion() string {
	return strings.TrimSpace(git(f.t, f.bare, "show", "develop:VERSION"))
}

func (f *repoFixture) remoteLog() string {
	return git(f.t, f.bare, "log", "--format=%s", "develop")
}

func TestRunBumpSemConcorrencia(t *testing.T) {
	f := newFixture(t)
	err := runBump(context.Background(), f.fetcher(nil), []string{"VERSION"}, []string{"plain"},
		"2026.11.17.5", "Bump versão para 2026.11.17.5", maxBumpAttempts)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.remoteVersion(); got != "2026.11.17.5" {
		t.Errorf("versão no remoto = %s", got)
	}
	if !strings.HasPrefix(f.remoteLog(), "Bump versão para 2026.11.17.5\n") {
		t.Errorf("mensagem do commit inesperada:\n%s", f.remoteLog())
	}
}

func TestRunBumpRefazDepoisDeNonFastForward(t *testing.T) {
	f := newFixture(t)
	err := runBump(context.Background(), f.fetcher(func(n int) {
		if n == 1 {
			f.avanca("") // outra MR entra entre o fetch e o push
		}
	}), []string{"VERSION"}, []string{"plain"}, "2026.11.17.5", "Bump versão para 2026.11.17.5", maxBumpAttempts)
	if err != nil {
		t.Fatal(err)
	}
	if f.fetchs != 2 {
		t.Errorf("fetchs = %d, quer 2", f.fetchs)
	}
	log := f.remoteLog()
	if !strings.Contains(log, "outra MR") || !strings.HasPrefix(log, "Bump versão para 2026.11.17.5\n") {
		t.Errorf("o bump deveria estar sobre o commit da outra MR:\n%s", log)
	}
	if f.remoteVersion() != "2026.11.17.5" {
		t.Errorf("versão = %s", f.remoteVersion())
	}
}

func TestRunBumpForaDeOrdemNaoRegride(t *testing.T) {
	f := newFixture(t)
	// A pipeline mais nova (.9) entra na develop enquanto a mais antiga (.5) ainda empurra.
	err := runBump(context.Background(), f.fetcher(func(n int) {
		if n == 1 {
			f.avanca("2026.11.17.9")
		}
	}), []string{"VERSION"}, []string{"plain"}, "2026.11.17.5", "Bump versão para 2026.11.17.5", maxBumpAttempts)
	if err != nil {
		t.Fatalf("job deveria ficar verde: %v", err)
	}
	if got := f.remoteVersion(); got != "2026.11.17.9" {
		t.Errorf("a versão regrediu para %s", got)
	}
	if strings.Contains(f.remoteLog(), "Bump versão para 2026.11.17.5") {
		t.Errorf("não deveria haver commit de bump da versão antiga:\n%s", f.remoteLog())
	}
}

func TestRunBumpVersaoIgualNaoCriaCommit(t *testing.T) {
	f := newFixture(t)
	antes := f.remoteLog()
	err := runBump(context.Background(), f.fetcher(nil), []string{"VERSION"}, []string{"plain"},
		"2026.11.17.1", "Bump versão para 2026.11.17.1", maxBumpAttempts)
	if err != nil {
		t.Fatal(err)
	}
	if f.remoteLog() != antes {
		t.Errorf("commit vazio criado:\n%s", f.remoteLog())
	}
}

func TestRunBumpEsgotaTentativas(t *testing.T) {
	f := newFixture(t)
	err := runBump(context.Background(), f.fetcher(func(n int) { f.avanca("") }),
		[]string{"VERSION"}, []string{"plain"}, "2026.11.17.5", "Bump versão para 2026.11.17.5", 3)
	if err == nil || !strings.Contains(err.Error(), "após 3 tentativas") {
		t.Fatalf("erro = %v", err)
	}
	if f.fetchs != 3 {
		t.Errorf("fetchs = %d, quer 3", f.fetchs)
	}
}

type falhaFixa struct{ err error }

func (falhaFixa) Read(context.Context, string) (string, error) { return "1.0.0\n", nil }
func (a falhaFixa) CommitAndPush(context.Context, []fileChange, string) error {
	return a.err
}

func TestRunBumpOutraFalhaDePushNaoRepete(t *testing.T) {
	n := 0
	fetch := func(context.Context) (bumpAttempt, error) {
		n++
		return falhaFixa{errors.New("! [remote rejected] HEAD -> develop (pre-receive hook declined)")}, nil
	}
	err := runBump(context.Background(), fetch, []string{"VERSION"}, []string{"plain"}, "2026.11.17.5", "m", maxBumpAttempts)
	if err == nil || n != 1 {
		t.Fatalf("err=%v tentativas=%d", err, n)
	}
}

func TestIsPushRejected(t *testing.T) {
	sim := []string{
		"! [rejected] HEAD -> develop (non-fast-forward)",
		"! [rejected] HEAD -> develop (fetch first)",
		"hint: Updates were rejected because the remote contains work that you do not have locally",
	}
	for _, s := range sim {
		if !isPushRejected(errors.New(s)) {
			t.Errorf("deveria reconhecer: %s", s)
		}
	}
	if isPushRejected(errors.New("fatal: Authentication failed")) {
		t.Error("autenticação não deve repetir")
	}
}

func TestBumpPushUsaCiSkip(t *testing.T) {
	got := strings.Join(bumpPushArgs("url", "develop"), " ")
	if got != "git push -o ci.skip url HEAD:develop" {
		t.Errorf("push = %s", got)
	}
}

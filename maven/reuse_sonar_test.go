package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"dagger/maven/internal/dagger"
)

// Compara, no Sonar, o fluxo antigo (FullBuild com análise) com o novo (FullBuild sem análise,
// AnalysisInputs, AnalyzeFromReports) sobre o mesmo código. Precisa de um SonarQube descartável:
//
//	TG278_ENGINE_TESTS=1
//	TG278_SONAR_URL=http://172.17.0.1:9100        como o engine do Dagger o enxerga
//	TG278_SONAR_API=http://localhost:9100         como o teste o enxerga
//	TG278_SONAR_TOKEN_FILE=<arquivo com um token de usuário>
//	TG278_TRIAGEM_ARCHIVE=<tar.gz do triagem.ai>  opcional: liga o caso do reactor
//
// Nunca aponte para um Sonar de projeto real: as chaves usadas são `tg278-cmp-*`.

var comparedMetrics = "tests,test_failures,coverage,line_coverage,lines_to_cover,ncloc,files"

type sonarEnv struct{ engineURL, apiURL, token string }

func sonarFromEnv(t *testing.T) sonarEnv {
	t.Helper()
	engineTest(t)
	e := sonarEnv{engineURL: os.Getenv("TG278_SONAR_URL"), apiURL: os.Getenv("TG278_SONAR_API")}
	file := os.Getenv("TG278_SONAR_TOKEN_FILE")
	if e.engineURL == "" || e.apiURL == "" || file == "" {
		t.Skip("defina TG278_SONAR_URL, TG278_SONAR_API e TG278_SONAR_TOKEN_FILE (Sonar descartável)")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	e.token = strings.TrimSpace(string(raw))
	return e
}

func (e sonarEnv) measures(t *testing.T, key string) map[string]string {
	t.Helper()
	req, _ := http.NewRequest("GET", e.apiURL+"/api/measures/component?component="+url.QueryEscape(key)+"&metricKeys="+comparedMetrics, nil)
	req.SetBasicAuth(e.token, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Component struct {
			Measures []struct{ Metric, Value string }
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range body.Component.Measures {
		out[m.Metric] = m.Value
	}
	return out
}

// compareFlows roda os dois fluxos e exige as mesmas medidas. wantTests diz se o caso tem de ter
// medida de testes (os dois lados): sem isso dois fluxos igualmente vazios passariam.
func compareFlows(t *testing.T, e sonarEnv, name string, m *Maven, source *dagger.Directory, module string, wantTests bool) {
	t.Helper()
	ctx := context.Background()
	token := dag.SetSecret("tg278-cmp-token-"+name, e.token)
	oldKey, newKey := "tg278-cmp-"+name+"-old", "tg278-cmp-"+name+"-new"

	oldCfg, err := m.NewSonarConfig(e.engineURL, token, true, nil, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	oldBuilt, err := m.FullBuild(ctx, source, module, "", "", oldCfg, nil, nil, "")
	if err != nil {
		t.Fatalf("fluxo antigo: %v", err)
	}
	// O mesmo build serve de base ao novo, sem a análise: o que se compara é a análise.
	m2 := New(m.Image, m.UseMvnw, m.UseCache, m.UseDefaultCiOptions, m.ExtraOptions, m.ReactorMode, m.UseJib, m.UseDocker, "", nil)
	built, err := m2.FullBuild(ctx, source, module, "", "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("build do fluxo novo: %v", err)
	}
	newCfg, _ := m2.NewSonarConfig(e.engineURL, token, true, nil, newKey)
	result, err := m2.AnalyzeFromReports(ctx, source, module, m2.AnalysisInputs(built.Tree), newCfg, "")
	if err != nil {
		t.Fatalf("fluxo novo: %v", err)
	}
	for _, out := range result.Stdout {
		if strings.Contains(out, "Tests run:") {
			t.Fatal("a análise do fluxo novo executou testes")
		}
	}
	_ = oldBuilt

	oldM, newM := e.measures(t, oldKey), e.measures(t, newKey)
	t.Logf("%s: antigo=%v novo=%v", name, oldM, newM)
	if wantTests && (oldM["tests"] == "" || newM["tests"] == "") {
		t.Errorf("%s: faltou a medida de testes (antigo=%q novo=%q)", name, oldM["tests"], newM["tests"])
	}
	if !reflect.DeepEqual(oldM, newM) {
		t.Errorf("%s: medidas diferem\n antigo: %v\n  novo: %v", name, oldM, newM)
	}
}

func fixtureMaven(opts ...string) *Maven {
	return New(mavenFixtureImage, false, true, true, opts, false, false, false, "", nil)
}

func TestEngineSonarAggregate(t *testing.T) {
	e := sonarFromEnv(t)
	compareFlows(t, e, "aggregate", fixtureMaven(), aggregateSource(), "aggregate", true)
}

func TestEngineSonarCustomPaths(t *testing.T) {
	e := sonarFromEnv(t)
	compareFlows(t, e, "custom", fixtureMaven(), customPathsSource(), "custompaths", true)
}

// O reactor do triagem: build -pl triagem-core -am com testes unitários selecionados (sem Docker).
func TestEngineSonarTriagemReactor(t *testing.T) {
	e := sonarFromEnv(t)
	archive := os.Getenv("TG278_TRIAGEM_ARCHIVE")
	if archive == "" {
		t.Skip("defina TG278_TRIAGEM_ARCHIVE")
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	source := dag.Container().From(mavenFixtureImage).
		WithNewFile("/source.b64", base64.StdEncoding.EncodeToString(raw)).
		WithExec([]string{"sh", "-c", "mkdir /source; base64 -d /source.b64 | tar -xz -C /source"}).
		Directory("/source")
	opts := []string{"-Dtest=CurriculoRecebidoV1Test,TriagemExceptionTest,WikiDoJiraTest", "-Dsurefire.failIfNoSpecifiedTests=false"}
	m := New(mavenFixtureImage, false, true, true, opts, true, false, false, "", nil)
	compareFlows(t, e, "triagem", m, source, "triagem-core", true)
}

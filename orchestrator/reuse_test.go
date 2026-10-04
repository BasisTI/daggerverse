package main

import (
	"reflect"
	"testing"

	"github.com/BasisTI/daggerverse/pipeline/config"
)

const mixedConfig = `
schema-version = 1

[project]
group = "demo"

[targets.api]
type = "maven"
sonar = true

[targets.batch]
type = "maven"

[targets.web]
type = "npm"
sonar = true

[targets.worker]
type = "dockerfile"
dockerfile = "worker/Dockerfile"
quality-type = "uv"
sonar = true
`

func loadMixed(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load([]byte(mixedConfig))
	if err != nil {
		t.Fatalf("carregar config: %v", err)
	}
	return cfg
}

// Só o Maven analisado como Maven deixa relatórios: sem sonar não há quem os leia, e npm, uv e
// Dockerfile continuam no check completo.
func TestCollectsReports(t *testing.T) {
	cfg := loadMixed(t)
	want := map[string]bool{"api": true, "batch": false, "web": false, "worker": false}
	for name, expected := range want {
		rt, err := cfg.Resolve(name)
		if err != nil {
			t.Fatalf("resolver %s: %v", name, err)
		}
		if got := collectsReports(rt); got != expected {
			t.Errorf("%s: collectsReports = %t, quer %t", name, got, expected)
		}
	}
}

func TestWithReusedReportsPartitionsTargets(t *testing.T) {
	cfg := loadMixed(t)

	t.Run("com relatório do api", func(t *testing.T) {
		targets, err := qualityTargets(cfg, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		reused, fallback, err := withReusedReports(cfg, targets, map[string]bool{"api": true, "web": true}, dag.Directory(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		// web tem entrada no diretório mas é npm: não é reaproveitável.
		if want := []string{"api"}; !reflect.DeepEqual(reused, want) {
			t.Errorf("reaproveitados = %v, quer %v", reused, want)
		}
		if want := []string{"web", "worker"}; !reflect.DeepEqual(fallback, want) {
			t.Errorf("check completo = %v, quer %v", fallback, want)
		}
	})

	t.Run("sem diretório de relatórios", func(t *testing.T) {
		targets, err := qualityTargets(cfg, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		reused, fallback, err := withReusedReports(cfg, targets, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(reused) != 0 {
			t.Errorf("reaproveitados = %v, quer nenhum", reused)
		}
		if want := []string{"api", "web", "worker"}; !reflect.DeepEqual(fallback, want) {
			t.Errorf("check completo = %v, quer %v", fallback, want)
		}
	})
}

func TestReportCollectorKeepsFirstSeenOrder(t *testing.T) {
	c := newReportCollector()
	c.add("b", nil)
	c.add("a", nil)
	c.add("b", nil)
	if want := []string{"b", "a"}; !reflect.DeepEqual(c.order, want) {
		t.Errorf("ordem = %v, quer %v", c.order, want)
	}
}

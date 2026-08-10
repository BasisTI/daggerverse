package main

import (
	"reflect"
	"testing"
)

func TestMatchChangedProjects(t *testing.T) {
	tests := []struct {
		name         string
		files        []string
		projectPaths map[string]string
		want         []string
	}{
		{
			// O caso que motivou o helper: um app Maven de módulo único ocupa a raiz, e
			// `git diff --name-only` devolve "pom.xml", não "./pom.xml".
			name:         "raiz casa qualquer arquivo",
			files:        []string{"pom.xml", "src/main/java/App.java"},
			projectPaths: map[string]string{"ponto": "."},
			want:         []string{"ponto"},
		},
		{
			name:         "path vazio equivale à raiz",
			files:        []string{"qualquer/coisa.txt"},
			projectPaths: map[string]string{"app": ""},
			want:         []string{"app"},
		},
		{
			name:  "prefixo comum seleciona só o target alterado",
			files: []string{"apps/beneficios/pom.xml"},
			projectPaths: map[string]string{
				"beneficios": "apps/beneficios",
				"frontend":   "frontend",
			},
			want: []string{"beneficios"},
		},
		{
			name:         "sem arquivos alterados não afeta ninguém",
			files:        []string{""},
			projectPaths: map[string]string{"ponto": "."},
			want:         []string{},
		},
		{
			name:  "um arquivo pode afetar mais de um target",
			files: []string{"libs/comum/Util.java"},
			projectPaths: map[string]string{
				"a": "libs/comum",
				"b": "libs",
			},
			want: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchChangedProjects(tt.files, tt.projectPaths)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("matchChangedProjects() = %v, quero %v", got, tt.want)
			}
		})
	}
}

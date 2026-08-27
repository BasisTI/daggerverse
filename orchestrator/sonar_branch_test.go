package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestAnalysisOptionsModes cobre os três modos de análise e, principalmente, a combinação que não
// pode existir.
//
// O motivo de a combinação ser erro e não precedência: `-Dsonar.branch.name` e
// `-Dsonar.pullrequest.*` mandam o servidor gravar em lugares diferentes. Escolher um dos dois
// silenciosamente faria a pipeline analisar certo e reportar no lugar errado, que é exatamente o
// tipo de falha que só aparece semanas depois, quando alguém estranha um número no dashboard.
func TestAnalysisOptionsModes(t *testing.T) {
	tests := []struct {
		name        string
		sonarBranch string
		mrId        string
		mrSource    string
		mrTarget    string
		want        []string
		wantErr     string
	}{
		{
			name:        "análise de branch",
			sonarBranch: "develop",
			want:        []string{"-Dsonar.branch.name=develop"},
		},
		{
			name:     "análise de pull request",
			mrId:     "37",
			mrSource: "TG-70",
			mrTarget: "develop",
			want: []string{
				"-Dsonar.pullrequest.key=37",
				"-Dsonar.pullrequest.branch=TG-70",
				"-Dsonar.pullrequest.base=develop",
			},
		},
		{
			// Sem nenhum dos dois a análise cai na branch principal do projeto. É o
			// comportamento anterior à 3.11.0 e continua válido, mas agora avisa.
			name: "sem modo declarado",
			want: nil,
		},
		{
			// Análise de PR pela metade é rejeitada pelo scanner; cair na branch principal é o
			// comportamento correto, e é o que já acontecia antes desta mudança.
			name:     "parâmetros de MR incompletos",
			mrId:     "37",
			mrSource: "TG-70",
			want:     nil,
		},
		{
			name:        "branch e merge request juntos",
			sonarBranch: "develop",
			mrId:        "37",
			mrSource:    "TG-70",
			mrTarget:    "develop",
			wantErr:     "mutuamente exclusivos",
		},
		{
			// Basta UM parâmetro de MR presente para a combinação ser recusada: com os três
			// incompletos o pullRequestOptions devolveria nil e o conflito passaria batido.
			name:        "branch com merge request incompleta",
			sonarBranch: "develop",
			mrTarget:    "develop",
			wantErr:     "mutuamente exclusivos",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := analysisOptions(tc.sonarBranch, tc.mrId, tc.mrSource, tc.mrTarget)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("esperava erro contendo %q, obteve opções %v", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("erro = %q, quer conter %q", err, tc.wantErr)
				}
				if got != nil {
					t.Errorf("erro deve vir sem opções, obteve %v", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("analysisOptions: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("opções = %v, quer %v", got, tc.want)
			}
		})
	}
}

func TestBranchOptions(t *testing.T) {
	if got := branchOptions(""); got != nil {
		t.Errorf("branch vazia deve devolver nil, obteve %v", got)
	}
	if got, want := branchOptions("main"), []string{"-Dsonar.branch.name=main"}; !reflect.DeepEqual(got, want) {
		t.Errorf("branchOptions = %v, quer %v", got, want)
	}
}

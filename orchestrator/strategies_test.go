package main

import (
	"reflect"
	"testing"
)

func TestPullRequestOptions(t *testing.T) {
	tests := []struct {
		name              string
		key, branch, base string
		want              []string
	}{
		{
			name: "trio completo vira análise de PR",
			key:  "301", branch: "feat/x", base: "develop",
			want: []string{
				"-Dsonar.pullrequest.key=301",
				"-Dsonar.pullrequest.branch=feat/x",
				"-Dsonar.pullrequest.base=develop",
			},
		},
		// Uma análise de PR pela metade é rejeitada pelo scanner: é melhor cair na
		// análise de branch do que quebrar o check.
		{name: "sem key", branch: "feat/x", base: "develop"},
		{name: "sem branch de origem", key: "301", base: "develop"},
		{name: "sem branch de destino", key: "301", branch: "feat/x"},
		{name: "fora de MR não passa nada"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pullRequestOptions(tt.key, tt.branch, tt.base)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("pullRequestOptions() = %v, quer %v", got, tt.want)
			}
		})
	}
}

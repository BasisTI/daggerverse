package main

import "testing"

// TestWorkdir trava as duas configurações do módulo, que agora coexistem.
//
// O build (publish) recebe o projeto já recortado pelo PublishAll e roda com ModulePath vazio --
// tem de continuar em /app exatamente como antes, senão a imagem publicada muda de layout. O
// check de qualidade recebe a raiz do repositório, para que o .git chegue ao scanner, e é o
// ModulePath que diz qual dos targets ali dentro é o alvo.
func TestWorkdir(t *testing.T) {
	tests := []struct {
		name       string
		modulePath string
		want       string
	}{
		{
			name: "sem ModulePath: Source já é o projeto",
			want: "/app",
		},
		{
			name:       "com ModulePath: Source é a raiz do repositório",
			modulePath: "scrapers/comprasnet_nodriver",
			want:       "/app/scrapers/comprasnet_nodriver",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := &Uv{ModulePath: tc.modulePath}
			if got := u.Workdir(); got != tc.want {
				t.Errorf("Workdir() = %q, quer %q", got, tc.want)
			}
		})
	}
}

// TestJoinPath cobre a borda que interessa: subdiretório vazio não pode virar uma barra sobrando.
// É o mesmo helper que monta o workdir do scanner a partir do WorkDir do SonarConfig, onde
// "/usr/src/" com barra final mandaria o scanner analisar o lugar errado.
func TestJoinPath(t *testing.T) {
	if got, want := joinPath("/usr/src", ""), "/usr/src"; got != want {
		t.Errorf("joinPath sem sub = %q, quer %q", got, want)
	}
	if got, want := joinPath("/usr/src", "scrapers/mte"), "/usr/src/scrapers/mte"; got != want {
		t.Errorf("joinPath = %q, quer %q", got, want)
	}
}

package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"dagger/orchestrator/internal/dagger"
)

func TestSecurityDataPlan(t *testing.T) {
	secret := dag.SetSecret("teste", "x")
	cases := []struct {
		nome      string
		cred      registryCredential
		useImage  bool
		wantNotes string
	}{
		{"com credencial", registryCredential{user: "u", password: secret}, true, ""},
		{"sem nada", registryCredential{}, false, "Sem credencial do registro"},
		{"só usuário", registryCredential{user: "u"}, false, "Sem credencial do registro"},
		{"só senha", registryCredential{password: secret}, false, "Sem credencial do registro"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			use, notice := securityDataPlan(c.cred)
			if use != c.useImage {
				t.Errorf("useImage = %v, quer %v", use, c.useImage)
			}
			if c.wantNotes == "" && notice != "" {
				t.Errorf("aviso inesperado: %q", notice)
			}
			if !strings.Contains(notice, c.wantNotes) {
				t.Errorf("aviso = %q, quer conter %q", notice, c.wantNotes)
			}
		})
	}
}

func TestIsImageMissing(t *testing.T) {
	cases := map[string]bool{
		"MANIFEST_UNKNOWN: manifest unknown":                 true,
		"docker.io/x:13: not found":                          true,
		"GET https://r/v2/x/manifests/13: 404":               true,
		"unauthorized: authentication required":              false,
		"GET https://r/v2/: 401 Unauthorized":                false,
		"denied: requested access to the resource is denied": false,
		"dial tcp: lookup r: no such host":                   false,
		"repository not found, access denied":                false,
	}
	for msg, want := range cases {
		if got := isImageMissing(errors.New(msg)); got != want {
			t.Errorf("isImageMissing(%q) = %v, quer %v", msg, got, want)
		}
	}
	if isImageMissing(nil) {
		t.Error("nil não é imagem ausente")
	}
}

// TestSecurityCheckPlumbing trava, por varredura do código, o que o teste de unidade não alcança:
// que o scan recebe a base e que ela não vai ao Maven sem credencial.
func TestSecurityCheckPlumbing(t *testing.T) {
	read := func(f string) string {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if !strings.Contains(read("strategies.go"), "SecurityData: securityData") {
		t.Error("securityMaven não passa a base ao SecurityScan")
	}
	if !strings.Contains(read("main.go"), "securityDataPlan(cred)") {
		t.Error("SecurityCheck não decide o caminho da base por securityDataPlan")
	}
	if !strings.Contains(read("security_data.go"), `"basis-registry.basis.com.br/basis/iac/nvd-data/odc-data:13"`) {
		t.Error("imagem padrão da base mudou")
	}
}

var _ *dagger.Secret

package imageref

import (
	"testing"
	"time"
)

func TestRef(t *testing.T) {
	tests := []struct {
		name                        string
		registry, group, image, tag string
		want                        string
	}{
		{
			name:     "completo",
			registry: "basis-registry.basis.com.br",
			group:    "triagem",
			image:    "triagem-core",
			tag:      "1.2.3",
			want:     "basis-registry.basis.com.br/triagem/triagem-core:1.2.3",
		},
		{
			name:     "tag vazia cai em latest",
			registry: "basis-registry.basis.com.br",
			group:    "licitacao",
			image:    "frontend",
			want:     "basis-registry.basis.com.br/licitacao/frontend:latest",
		},
		{
			name:     "sem grupo",
			registry: "docker.io",
			image:    "app",
			tag:      "v1",
			want:     "docker.io/app:v1",
		},
		{
			name:     "sem imagem",
			registry: "docker.io",
			group:    "grupo",
			tag:      "v1",
			want:     "docker.io/grupo:v1",
		},
		{
			name:  "sem registry",
			group: "grupo",
			image: "app",
			tag:   "v1",
			want:  "grupo/app:v1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Ref(tc.registry, tc.group, tc.image, tc.tag); got != tc.want {
				t.Errorf("Ref() = %q, quer %q", got, tc.want)
			}
		})
	}
}

func TestOCILabels(t *testing.T) {
	labels := OCILabels("abc123", "1.2.3")
	if got := labels["org.opencontainers.image.version"]; got != "1.2.3" {
		t.Errorf("version = %q", got)
	}
	if got := labels["org.opencontainers.image.revision"]; got != "abc123" {
		t.Errorf("revision = %q", got)
	}
	created := labels["org.opencontainers.image.created"]
	if _, err := time.Parse(time.RFC3339, created); err != nil {
		t.Errorf("created %q não é RFC3339: %v", created, err)
	}

	withoutSha := OCILabels("", "1.2.3")
	if _, ok := withoutSha["org.opencontainers.image.revision"]; ok {
		t.Error("revision não deveria existir sem commitSha")
	}
	if len(withoutSha) != 2 {
		t.Errorf("esperava 2 labels sem commitSha, obteve %v", withoutSha)
	}
}

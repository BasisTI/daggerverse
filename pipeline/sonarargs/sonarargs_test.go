package sonarargs

import (
	"reflect"
	"testing"
)

func TestBuildOptions(t *testing.T) {
	tests := []struct {
		name               string
		host               string
		token              string
		projectKey         string
		waitForQualityGate bool
		extra              []string
		want               []string
	}{
		{
			name:  "apenas token",
			token: "t0k3n",
			want:  []string{"-Dsonar.token=t0k3n"},
		},
		{
			name:               "completo",
			host:               "https://sonar.basis.com.br",
			token:              "t0k3n",
			projectKey:         "grupo:app",
			waitForQualityGate: true,
			extra:              []string{"-Dsonar.sources=src", "-Dsonar.tests=test"},
			want: []string{
				"-Dsonar.host.url=https://sonar.basis.com.br",
				"-Dsonar.token=t0k3n",
				"-Dsonar.projectKey=grupo:app",
				"-Dsonar.qualitygate.wait=true",
				"-Dsonar.sources=src",
				"-Dsonar.tests=test",
			},
		},
		{
			name:  "host vazio é omitido",
			token: "t0k3n",
			extra: nil,
			want:  []string{"-Dsonar.token=t0k3n"},
		},
		{
			name:       "projectKey sem quality gate",
			host:       "https://sonar",
			token:      "t",
			projectKey: "k",
			want: []string{
				"-Dsonar.host.url=https://sonar",
				"-Dsonar.token=t",
				"-Dsonar.projectKey=k",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildOptions(tc.host, tc.token, tc.projectKey, tc.waitForQualityGate, tc.extra)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("BuildOptions() = %v, quer %v", got, tc.want)
			}
		})
	}
}

// O token é sempre emitido, mesmo vazio — igual às três implementações originais.
func TestBuildOptionsAlwaysEmitsToken(t *testing.T) {
	got := BuildOptions("", "", "", false, nil)
	if want := []string{"-Dsonar.token="}; !reflect.DeepEqual(got, want) {
		t.Errorf("BuildOptions() = %v, quer %v", got, want)
	}
}

// Package sonarargs centraliza a montagem das opções de linha de comando de
// análise Sonar, hoje duplicada nos módulos maven, npm e uv.
package sonarargs

import "fmt"

// BuildOptions monta as opções `-Dsonar.*` da análise, na ordem canônica:
// host.url, token, projectKey, qualitygate.wait e por fim as opções extras.
//
// Opções vazias são omitidas, com uma exceção: o token é sempre emitido (mesmo
// vazio), reproduzindo o comportamento atual dos três módulos — lá o token vem
// de um *dagger.Secret obrigatório e é adicionado sem checagem de vazio.
//
// As três implementações originais (maven/sonar.go, npm/main.go, uv/main.go)
// produzem exatamente a mesma lista de opções; elas só divergiam no tratamento
// de config == nil (maven e npm devolviam (nil, nil); uv devolvia erro) e na
// leitura do Plaintext() do Secret. Ambas as responsabilidades ficam com o
// adapter de cada módulo, que decide se chama esta função ou não — daí a
// assinatura receber o token já em texto plano e não retornar erro.
func BuildOptions(host, token, projectKey string, waitForQualityGate bool, extra []string) []string {
	var options []string

	if host != "" {
		options = append(options, fmt.Sprintf("-Dsonar.host.url=%s", host))
	}

	options = append(options, fmt.Sprintf("-Dsonar.token=%s", token))

	if projectKey != "" {
		options = append(options, fmt.Sprintf("-Dsonar.projectKey=%s", projectKey))
	}

	if waitForQualityGate {
		options = append(options, "-Dsonar.qualitygate.wait=true")
	}

	options = append(options, extra...)

	return options
}

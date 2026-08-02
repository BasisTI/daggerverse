// Package imageref centraliza a montagem de referências de imagem no registry
// e dos labels OCI aplicados às imagens publicadas.
package imageref

import "time"

// DefaultTag é usada quando nenhuma tag é informada, reproduzindo o
// `imageReference("latest")` dos módulos npm/uv.
const DefaultTag = "latest"

// Ref monta a referência completa da imagem: "<registry>/<group>/<image>:<tag>".
//
// Reproduz DockerBuildConfig.imageReference de npm/types.go e uv/types.go:
// group e image são omitidos quando vazios, e tag vazia cai em DefaultTag.
func Ref(registry, group, image, tag string) string {
	ref := registry
	if group != "" {
		ref = join(ref, group)
	}
	if image != "" {
		ref = join(ref, image)
	}
	if tag == "" {
		tag = DefaultTag
	}
	return ref + ":" + tag
}

func join(base, segment string) string {
	if base == "" {
		return segment
	}
	return base + "/" + segment
}

// OCILabels devolve os labels org.opencontainers.image.* aplicados às imagens
// publicadas pelos módulos npm, uv e maven (via -Djib.container.labels):
// version, created (RFC3339, momento da chamada) e revision — este último
// apenas quando commitSha não é vazio.
func OCILabels(commitSha, version string) map[string]string {
	labels := map[string]string{
		"org.opencontainers.image.version": version,
		"org.opencontainers.image.created": time.Now().Format(time.RFC3339),
	}
	if commitSha != "" {
		labels["org.opencontainers.image.revision"] = commitSha
	}
	return labels
}

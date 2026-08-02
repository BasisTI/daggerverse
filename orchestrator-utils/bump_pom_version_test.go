package main

import (
	"strings"
	"testing"
)

const plainPom = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>ai.triagem</groupId>
  <artifactId>triagem-core</artifactId>
  <version>2026.01.01.1</version>
  <dependencies>
    <dependency>
      <groupId>ai.triagem</groupId>
      <artifactId>triagem-contracts</artifactId>
      <version>1.0.0</version>
    </dependency>
  </dependencies>
</project>
`

const revisionPom = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>ai.triagem</groupId>
  <artifactId>triagem-parent</artifactId>
  <version>${revision}</version>
  <packaging>pom</packaging>
  <properties>
    <revision>2026.01.01.1</revision>
    <java.version>25</java.version>
  </properties>
  <modules>
    <module>triagem-contracts</module>
    <module>triagem-core</module>
  </modules>
</project>
`

const revisionPomWithoutProperty = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>ai.triagem</groupId>
  <artifactId>triagem-parent</artifactId>
  <version>${revision}</version>
  <packaging>pom</packaging>
  <properties>
    <java.version>25</java.version>
  </properties>
</project>
`

func TestBumpPomVersionPlain(t *testing.T) {
	got, err := bumpPomVersion(plainPom, "2026.02.02.7")
	if err != nil {
		t.Fatalf("bumpPomVersion: %v", err)
	}
	if !strings.Contains(got, "<version>2026.02.02.7</version>") {
		t.Fatalf("project version was not bumped:\n%s", got)
	}
	// A versão da dependência não é a do projeto e tem de ficar intacta.
	if !strings.Contains(got, "<artifactId>triagem-contracts</artifactId>\n      <version>1.0.0</version>") {
		t.Fatalf("dependency version was touched:\n%s", got)
	}
	if strings.Contains(got, "<version>2026.01.01.1</version>") {
		t.Fatalf("old project version still present:\n%s", got)
	}
}

func TestBumpPomVersionRevisionProperty(t *testing.T) {
	got, err := bumpPomVersion(revisionPom, "2026.02.02.7")
	if err != nil {
		t.Fatalf("bumpPomVersion: %v", err)
	}
	// O placeholder tem de sobreviver: é ele que propaga a versão aos módulos.
	if !strings.Contains(got, "<version>${revision}</version>") {
		t.Fatalf("${revision} placeholder was overwritten:\n%s", got)
	}
	if !strings.Contains(got, "<revision>2026.02.02.7</revision>") {
		t.Fatalf("<revision> property was not bumped:\n%s", got)
	}
	if strings.Contains(got, "<revision>2026.01.01.1</revision>") {
		t.Fatalf("old revision still present:\n%s", got)
	}
	// Formatação preservada em todo o resto do arquivo.
	if !strings.Contains(got, "    <java.version>25</java.version>\n") {
		t.Fatalf("surrounding formatting was altered:\n%s", got)
	}
}

func TestBumpPomVersionRevisionWithoutProperty(t *testing.T) {
	_, err := bumpPomVersion(revisionPomWithoutProperty, "2026.02.02.7")
	if err == nil {
		t.Fatal("expected an error when ${revision} has no <revision> property")
	}
	if !strings.Contains(err.Error(), "<revision> property") {
		t.Fatalf("error should point at the missing <revision> property, got: %v", err)
	}
}

func TestBumpPomVersionWithoutVersion(t *testing.T) {
	_, err := bumpPomVersion(`<project><artifactId>x</artifactId></project>`, "1.0.0")
	if err == nil {
		t.Fatal("expected an error when the project has no <version>")
	}
}

func TestBumpFileVersionRoutesPomToRevision(t *testing.T) {
	got, err := bumpFileVersion(revisionPom, "2026.03.03.9", "pom.xml")
	if err != nil {
		t.Fatalf("bumpFileVersion: %v", err)
	}
	if !strings.Contains(got, "<revision>2026.03.03.9</revision>") {
		t.Fatalf("bumpFileVersion did not reach the revision property:\n%s", got)
	}
}

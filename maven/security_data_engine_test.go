package main

import (
	"context"
	"os"
	"testing"

	"dagger/maven/internal/dagger"
)

// Cold start do scan: sem cache de Maven (useCache=false) e com a base vinda de fora, o scan
// precisa receber -DdataDirectory/-DautoUpdate=false e ter a base montada. Roda com
// TG368_ENGINE_TESTS=1:
//
//	TG368_ENGINE_TESTS=1 dagger run go test -run TestEngineSecurityData ./...
//
// O pom da fixture não liga o Dependency-Check (a base real do ODC 13 só existe depois do bootstrap
// do nvd-data): o antrun falha o build se as properties ou a base não chegarem, e o exit code do
// SecurityScan é a prova.

const securityDataFixturePom = `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>test</groupId><artifactId>cold</artifactId><version>1.0</version>
<build><plugins><plugin><artifactId>maven-antrun-plugin</artifactId><version>3.1.0</version><executions><execution><phase>validate</phase><goals><goal>run</goal></goals><configuration><target>
<fail message="autoUpdate chegou como '${autoUpdate}'"><condition><not><equals arg1="${autoUpdate}" arg2="false"/></not></condition></fail>
<fail message="dataDirectory chegou como '${dataDirectory}'"><condition><not><equals arg1="${dataDirectory}" arg2="/odc-data"/></not></condition></fail>
<fail message="a base não está montada em /odc-data"><condition><not><available file="/odc-data/odc.mv.db"/></not></condition></fail>
</target></configuration></execution></executions></plugin></plugins></build></project>`

func scanFixtureExit(t *testing.T, data *dagger.Directory) int {
	t.Helper()
	ctx := context.Background()
	m := mustNew(New(ctx, mavenFixtureImage, false, false, true, nil, false, false, false, "", nil, "", "", nil))
	src := dag.Directory().WithNewFile("pom.xml", securityDataFixturePom)
	res, err := m.SecurityScan(ctx, src, "cold", "", data)
	if err != nil {
		t.Fatalf("SecurityScan: %v", err)
	}
	return res.ExitCode
}

func TestEngineSecurityDataChegaAoScan(t *testing.T) {
	engineTest2(t)
	data := dag.Directory().WithNewFile("odc.mv.db", "base-de-teste")
	if code := scanFixtureExit(t, data); code != 0 {
		t.Fatalf("exit = %d; as properties ou a base não chegaram ao mvn", code)
	}
}

func TestEngineSemSecurityDataNaoMexe(t *testing.T) {
	engineTest2(t)
	if code := scanFixtureExit(t, nil); code == 0 {
		t.Fatal("sem base a fixture deveria falhar (as properties não podem ser injetadas)")
	}
}

func engineTest2(t *testing.T) {
	t.Helper()
	if os.Getenv("TG368_ENGINE_TESTS") != "1" {
		t.Skip("defina TG368_ENGINE_TESTS=1 para rodar contra o engine do Dagger")
	}
}

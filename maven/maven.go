package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"encoding/xml"
	"fmt"
)

const DefaultMavenCacheName = "maven-cache"
const DefaultWorkdir = "/app"

type pomProject struct {
	// XMLName is used to ensure we're parsing the <project> element.
	XMLName xml.Name `xml:"project"`
	// Version captures the content of the <version> tag.
	Version string `xml:"version"`
}

func (mc *Maven) WithImage(image string) *Maven {
	mc.Image = image
	return mc
}

func (mc *Maven) WithUseMvnw(mvnw bool) *Maven {
	mc.UseMvnw = mvnw
	return mc
}

func (mc *Maven) WithUseCache(useCache bool) *Maven {
	mc.UseCache = useCache
	return mc
}

func (mc *Maven) WithUseDefaultCiOptions(useDefaultCiOptions bool) *Maven {
	mc.UseDefaultCiOptions = useDefaultCiOptions
	return mc
}

func (mc *Maven) WithDir(dir *dagger.Directory) *Maven {
	mc.Dir = dir
	return mc
}

func (mc *Maven) NewContainer() *dagger.Container {
	container := dag.Container().From(mc.Image).WithWorkdir(DefaultWorkdir)
	if mc.UseCache {
		container = container.WithMountedCache("/root/.m2", dag.CacheVolume(DefaultMavenCacheName))
	}
	if mc.Dir != nil {
		container = container.WithMountedDirectory(DefaultWorkdir, mc.Dir)
	}
	return container
}

func (mc *Maven) Container() *dagger.Container {
	if mc.MavenContainer == nil {
		mc.MavenContainer = mc.NewContainer()
	}
	return mc.MavenContainer
}

func (mc *Maven) MavenBuild(args []string) *Maven {
	container := mc.Container()
	var execCmd []string
	if mc.UseMvnw {
		execCmd = append(execCmd, "./mvnw")
	} else {
		execCmd = append(execCmd, "mvn")
	}
	if mc.UseDefaultCiOptions {
		execCmd = append(execCmd, DefaultMvnCiOptions...)
	}
	mc.MavenContainer = container.WithExec(append(execCmd, args...))
	return mc
}

func (mc *Maven) GetGeneratedArtifact(jarName string) *dagger.File {
	return mc.Container().File(fmt.Sprintf("%s/target/%s", DefaultWorkdir, jarName))
}

func (m *Maven) GetVersion(ctx context.Context) (string, error) {
	if m.Dir == nil {
		return "", fmt.Errorf("cannot get version: maven directory is not set")
	}
	pomXML, err := m.Dir.File("pom.xml").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to read pom.xml from directory: %w", err)
	}
	var project pomProject
	if err := xml.Unmarshal([]byte(pomXML), &project); err != nil {
		return "", fmt.Errorf("failed to parse pom.xml: %w", err)
	}
	if project.Version == "" {
		return "", fmt.Errorf("could not find a <version> tag inside the <project> tag in pom.xml")
	}
	return project.Version, nil
}

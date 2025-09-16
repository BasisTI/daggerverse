package main

import (
	"context"
	"dagger/maven/internal/dagger"
	"encoding/xml"
	"fmt"
)

const DefaultMavenCacheName = "maven-cache"
const BaseWorkdir = "/app"

type pomProject struct {
	// XMLName is used to ensure we're parsing the <project> element.
	XMLName xml.Name `xml:"project"`
	// Version captures the content of the <version> tag.
	Version string `xml:"version"`
}

func (m *Maven) WithImage(image string) *Maven {
	m.Image = image
	return m
}

func (m *Maven) WithUseMvnw(mvnw bool) *Maven {
	m.UseMvnw = mvnw
	return m
}

func (m *Maven) WithUseCache(useCache bool) *Maven {
	m.UseCache = useCache
	return m
}

func (m *Maven) WithUseDefaultCiOptions(useDefaultCiOptions bool) *Maven {
	m.UseDefaultCiOptions = useDefaultCiOptions
	return m
}

func (m *Maven) WithParentPom(parentPom *dagger.File) *Maven {
	m.ParentPom = parentPom
	return m
}

// Initialize base maven container. If there is a parent, add the pom in the base
// Working Directory
func (m *Maven) NewBaseContainer() *dagger.Container {
	container := dag.Container().From(m.Image).WithWorkdir(BaseWorkdir)
	if m.UseCache {
		container = container.WithMountedCache("/root/.m2", dag.CacheVolume(DefaultMavenCacheName))
	}
	if m.ParentPom != nil {
		container = container.
			WithFile(fmt.Sprintf("%s/%s", BaseWorkdir, "pom.xml"), m.ParentPom).
			WithWorkdir(BaseWorkdir).
			WithExec(m.getFullMvnModuleCommand([]string{"-N"}, []string{"install"}))
	}
	return container
}

func (m *Maven) Container() *dagger.Container {
	if m.BaseContainer == nil {
		m.BaseContainer = m.NewBaseContainer()
	}
	return m.BaseContainer
}

func (m *Maven) MavenBuild(goals []string) *Maven {
	m.BaseContainer = m.Container().WithExec(m.getFullMvnCommand(goals))
	return m
}

func (m *Maven) getFullMvnCommand(goals []string) []string {
	return m.getFullMvnModuleCommand(nil, goals)
}

func (m *Maven) getFullMvnModuleCommand(moduleOptions []string, goals []string) []string {
	var execCmd []string
	if m.UseMvnw {
		execCmd = append(execCmd, "./mvnw")
	} else {
		execCmd = append(execCmd, "mvn")
	}
	if m.UseDefaultCiOptions {
		execCmd = append(execCmd, DefaultMvnCiOptions...)
	}
	if m.ExtraOptions != nil {
		execCmd = append(execCmd, m.ExtraOptions...)
	}
	if moduleOptions != nil {
		execCmd = append(execCmd, moduleOptions...)
	}
	return append(execCmd, goals...)
}

// GetGeneratedArtifact returns an artifact from the target directory
func (m *Maven) GetGeneratedArtifact(jarName string) *dagger.File {
	return m.Container().File(fmt.Sprintf("%s/target/%s", BaseWorkdir, jarName))
}

// GetBuildDir returns the output build Directory
func (m *Maven) GetBuildDir() *dagger.Directory {
	return m.Container().Directory(BaseWorkdir)
}

// GetVersion returns the project version from pom.xml in the moduleDir
// If not set and there is a Parent, take from there
func (m *Maven) GetVersion(ctx context.Context, moduleDir *dagger.Directory) (string, error) {
	if moduleDir == nil {
		return "", fmt.Errorf("cannot get version: maven directory is not set")
	}
	project, err := m.readProject(ctx, moduleDir.File("pom.xml"))
	if err != nil {
		return "", err
	}
	if project.Version == "" {
		if m.ParentPom != nil {
			project, err = m.readProject(ctx, m.ParentPom)
		}
		if err != nil || project.Version == "" {
			return "", fmt.Errorf("could not find a <version> tag inside the <project> tag in pom.xml or Parent")
		}
	}
	return project.Version, nil
}

func (m *Maven) readProject(ctx context.Context, pomFile *dagger.File) (*pomProject, error) {
	pomXML, err := pomFile.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read pom.xml from directory: %w", err)
	}
	var project pomProject
	if err := xml.Unmarshal([]byte(pomXML), &project); err != nil {
		return nil, fmt.Errorf("failed to parse pom.xml: %w", err)
	}
	return &project, nil
}

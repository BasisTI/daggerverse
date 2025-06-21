package main

import (
	"context"
	"dagger/npm/internal/dagger"
	"encoding/json"
	"fmt"
)

const DefaultNpmCacheName = "npm-cache"
const DefaultWorkdir = "/app"

var NpmRumCmd = []string{"npm", "run"}

type Npm struct {
	Image         string
	UseCache      bool
	Dir           *dagger.Directory
	NodeContainer *dagger.Container
}

type packageJSON struct {
	Version string `json:"version"`
}

func New(
// Diretório com o código-fonte node/npm
	source *dagger.Directory,
// Image name for building application
// +default="node:22.16.0-alpine3.22"
	buildImage string,
// Use Npm Cache
// +default=true
	useCache bool) *Npm {
	return &Npm{
		Image:    buildImage,
		UseCache: useCache,
		Dir:      source,
	}
}

func (nc *Npm) WithImagem(image string) *Npm {
	nc.Image = image
	return nc
}

func (nc *Npm) WithUseCache(useCache bool) *Npm {
	nc.UseCache = useCache
	return nc
}

func (nc *Npm) WithDir(dir *dagger.Directory) *Npm {
	nc.Dir = dir
	return nc
}

func (nc *Npm) NewContainer() *dagger.Container {
	container := dag.Container().From(nc.Image).WithWorkdir(DefaultWorkdir)
	if nc.UseCache {
		container = container.WithMountedCache("/root/.npm", dag.CacheVolume(DefaultNpmCacheName))
	}
	if nc.Dir != nil {
		container = container.WithDirectory(DefaultWorkdir, nc.Dir)
	}
	return container
}

func (nc *Npm) Container() *dagger.Container {
	if nc.NodeContainer == nil {
		nc.NodeContainer = nc.NewContainer()
	}
	return nc.NodeContainer
}

func (nc *Npm) NpmRun(args []string) *Npm {
	container := nc.Container()
	nc.NodeContainer = container.WithExec(append(NpmRumCmd, args...))
	return nc
}

func (nc *Npm) GetAngularDistDir() *dagger.Directory {
	return nc.Container().Directory(fmt.Sprintf("%s/dist/browser", DefaultWorkdir))
}

func (n *Npm) GetVersion(ctx context.Context) (string, error) {
	if n.Dir == nil {
		return "", fmt.Errorf("cannot get NPM version: npm directory is not set")
	}
	pkgJSON, err := n.Dir.File("package.json").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to read package.json from directory: %w", err)
	}
	var pkg packageJSON
	if err := json.Unmarshal([]byte(pkgJSON), &pkg); err != nil {
		return "", fmt.Errorf("failed to parse package.json: %w", err)
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("could not find 'version' key in package.json")
	}
	return pkg.Version, nil
}

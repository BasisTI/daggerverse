package main

import (
	"fmt"

	"dagger/maven/internal/dagger"
)

const (
	// DindImage is the Docker-in-Docker image backing the daemon offered to builds.
	DindImage = "docker:29-dind"
	// DockerPort is the plain TCP port the daemon listens on. TLS is off on purpose: the daemon is
	// only reachable over Dagger's private service network, from the build container that asked
	// for it, and it is discarded when the build ends.
	DockerPort = 2375
	// DockerHostname is the alias the build container resolves the daemon by.
	DockerHostname = "docker"
	// DockerStorageCacheName identifies the volume mounted at the daemon's /var/lib/docker.
	DockerStorageCacheName = "maven-dind-storage"
)

// dockerService returns a Docker daemon for the build to talk to.
//
// Testcontainers needs a real daemon and the Maven image has none, so `mvn verify` fails every
// suite with "Could not find a valid Docker environment" — and, because the CI options include
// -Dmaven.test.failure.ignore=true, it fails without failing the build. Binding a daemon here
// fixes that for every project at once, and without touching test code: a suite written against
// Testcontainers runs unchanged.
func (m *Maven) dockerService() *dagger.Service {
	return dag.Container().
		From(DindImage).
		// Dagger provides pid 1, so tini warns on every run unless told it is not the reaper.
		WithEnvVariable("TINI_SUBREAPER", "true").
		// The daemon's storage has to live on a real volume. Left on the container filesystem it
		// lands on Dagger's own overlayfs, and every pull dies extracting the first whiteout
		// entry -- "failed to convert whiteout file ...: operation not permitted" -- because
		// overlayfs cannot create whiteouts on top of overlayfs.
		//
		// Private sharing, not Shared: two daemons writing the same storage would corrupt it.
		// Not Locked either, which would keep them from corrupting each other by making one wait
		// for the whole of the other's build. The cost is that images are pulled fresh per run.
		WithMountedCache("/var/lib/docker", dag.CacheVolume(DockerStorageCacheName),
			dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModePrivate}).
		WithExposedPort(DockerPort).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"dockerd",
				"--tls=false",
				fmt.Sprintf("--host=tcp://0.0.0.0:%d", DockerPort),
			},
			InsecureRootCapabilities: true,
			UseEntrypoint:            true,
		})
}

// withDocker points a build container at the daemon returned by dockerService.
//
// The binding is attached to the base container rather than to a single stage, so one daemon
// serves the whole build instead of being started and thrown away per stage.
func (m *Maven) withDocker(container *dagger.Container) *dagger.Container {
	return container.
		WithServiceBinding(DockerHostname, m.dockerService()).
		WithEnvVariable("DOCKER_HOST", fmt.Sprintf("tcp://%s:%d", DockerHostname, DockerPort)).
		// Ryuk is the Testcontainers reaper. It has nothing to reap here — the daemon dies with
		// the service — and reaching it back through the service binding is unreliable, so every
		// suite would pay its startup timeout for no gain.
		WithEnvVariable("TESTCONTAINERS_RYUK_DISABLED", "true")
}

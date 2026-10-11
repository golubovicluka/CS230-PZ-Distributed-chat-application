// CI pipeline for the distributed chat application. Every check runs the same
// way on a laptop (`dagger check`) and in GitHub Actions.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"dagger/chat/internal/dagger"
)

// Tool and base images are pinned by tag and digest. Renovate updates both
// through the "renovate: image" marker.
const (
	golangImage       = "golang:1.27.2@sha256:5bc7f572bbaa98885a3a1fd9c0aa76b59e3e14e8628bfc316bbfd0c701e4818c"                        // renovate: image
	golangciImage     = "golangci/golangci-lint:v2.14.0@sha256:ad862ba6b3798cbe0fd9fd7408d498fd74fbd2623a92406b2fd3898faf0bf98f"       // renovate: image
	nodeImage         = "node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1"                       // renovate: image
	pythonImage       = "python:3.13-alpine@sha256:2d9aefe2fef018a7eb2c13064c89c71929800fd2e5dccdbf52ea5da5bb8d929a"                   // renovate: image
	markdownlintImage = "davidanson/markdownlint-cli2:v0.23.3@sha256:d5f3f3f04b2e285dcbcdcd13b4454d119e273e3c393a9dabd163dba4abad526d" // renovate: image
	shellcheckImage   = "koalaman/shellcheck-alpine:v0.11.0@sha256:9955be09ea7f0dbf7ae942ac1f2094355bb30d96fffba0ec09f5432207544002"   // renovate: image
	actionlintImage   = "rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667"              // renovate: image
	gitleaksImage     = "zricethezav/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f"         // renovate: image
	trivyImage        = "aquasec/trivy:0.75.0@sha256:af6acf9a6b85dfe389a1941505c0ce9efef52a4719635e1a962f022a3d855daa"                 // renovate: image
	syftImage         = "anchore/syft:v1.54.1@sha256:3eb5379ba7b409c3f4069b686110527af0c47df993fa5c10d13e7cf34f49b1aa"                 // renovate: image
	grypeImage        = "anchore/grype:v0.120.1@sha256:e4a44ef45d285b829ce6efe2642980329661bd2d18eab5fc539138d4adaebbbe"               // renovate: image
	postgresImage     = "postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"                   // renovate: image
	redisImage        = "redis:8.8-alpine@sha256:0b2b77d3ea5078274795e3177cdbdada8b96316684a38911d528534ed679b5ec"                     // renovate: image

	pnpmVersion     = "12.11.2" // renovate: datasource=npm depName=pnpm
	yamllintVersion = "1.38.0"  // renovate: datasource=pypi depName=yamllint
)

var (
	components = []string{"chatserver", "loadbalancer", "web"}
	platforms  = []dagger.Platform{"linux/amd64", "linux/arm64"}
)

type Chat struct {
	// +private
	Source *dagger.Directory
}

func New(
	// Repository root
	// +defaultPath="/"
	// +ignore=["chat-app/node_modules", "chat-app/dist", "**/.DS_Store"]
	source *dagger.Directory,
) *Chat {
	return &Chat{Source: source}
}

// Lint Go code, check formatting and verify go.mod is tidy
// +check
func (m *Chat) GoLint(ctx context.Context) error {
	_, err := m.goContainer(golangciImage).
		WithMountedCache("/root/.cache/golangci-lint", dag.CacheVolume("chat-golangci-lint")).
		WithExec([]string{"golangci-lint", "run", "./..."}).
		WithExec([]string{"golangci-lint", "fmt", "--diff", "./..."}).
		WithExec([]string{"go", "mod", "tidy", "-diff"}).
		WithDirectory("/ci", m.Source.Directory("ci"), dagger.ContainerWithDirectoryOpts{Include: []string{"**/*.go"}}).
		WithExec([]string{"sh", "-c", `unformatted="$(gofmt -l /ci)"; if [ -n "$unformatted" ]; then gofmt -d /ci; exit 1; fi`}).
		Sync(ctx)
	return err
}

// Run every Go test with the race detector against real PostgreSQL and Redis
// +check
func (m *Chat) GoTest(ctx context.Context) error {
	postgres := dag.Container().
		From(postgresImage).
		WithEnvVariable("POSTGRES_USER", "chat").
		WithEnvVariable("POSTGRES_PASSWORD", "chat").
		WithEnvVariable("POSTGRES_DB", "chat").
		WithExposedPort(5432).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
	redis := dag.Container().
		From(redisImage).
		WithExposedPort(6379).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	_, err := m.goContainer(golangImage).
		WithServiceBinding("postgres", postgres).
		WithServiceBinding("redis", redis).
		WithEnvVariable("TEST_DATABASE_URL", "postgres://chat:chat@postgres:5432/chat?sslmode=disable").
		WithEnvVariable("TEST_REDIS_ADDR", "redis:6379").
		WithExec([]string{"go", "test", "-race", "-count=1", "./..."}).
		Sync(ctx)
	return err
}

// Lint, type-check and build the React frontend
// +check
func (m *Chat) Web(ctx context.Context) error {
	_, err := dag.Container().
		From(nodeImage).
		WithExec([]string{"npm", "install", "--global", "pnpm@" + pnpmVersion}).
		WithMountedCache("/root/.local/share/pnpm/store", dag.CacheVolume("chat-pnpm-store")).
		WithDirectory("/app", m.Source.Directory("chat-app")).
		WithWorkdir("/app").
		WithExec([]string{"pnpm", "install", "--frozen-lockfile"}).
		WithExec([]string{"pnpm", "lint"}).
		WithExec([]string{"pnpm", "exec", "tsc", "-b"}).
		WithExec([]string{"pnpm", "build"}).
		Sync(ctx)
	return err
}

// Lint every YAML file
// +check
func (m *Chat) Yaml(ctx context.Context) error {
	_, err := dag.Container().
		From(pythonImage).
		WithExec([]string{"pip", "install", "--no-cache-dir", "yamllint==" + yamllintVersion}).
		With(m.withSource).
		WithExec([]string{"yamllint", "--strict", "."}).
		Sync(ctx)
	return err
}

// Lint every Markdown file
// +check
func (m *Chat) Markdown(ctx context.Context) error {
	_, err := dag.Container().
		From(markdownlintImage).
		With(m.withSource).
		WithExec([]string{"markdownlint-cli2"}).
		Sync(ctx)
	return err
}

// Lint shell scripts and git hooks
// +check
func (m *Chat) Shell(ctx context.Context) error {
	_, err := dag.Container().
		From(shellcheckImage).
		With(m.withSource).
		WithExec([]string{"sh", "-c", "shellcheck scripts/*.sh .githooks/pre-commit"}).
		Sync(ctx)
	return err
}

// Lint GitHub Actions workflows
// +check
func (m *Chat) Workflows(ctx context.Context) error {
	_, err := dag.Container().
		From(actionlintImage).
		With(m.withSource).
		WithExec([]string{"actionlint", "-color"}).
		Sync(ctx)
	return err
}

// Scan the working tree and, when available, the git history for secrets
// +check
func (m *Chat) Secrets(ctx context.Context) error {
	_, err := dag.Container().
		From(gitleaksImage).
		WithUser("root").
		With(m.withSource).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "*"}).
		WithExec([]string{"sh", "-c", `
set -e
gitleaks dir --redact --no-banner .
if [ -d .git ]; then
  gitleaks git --redact --no-banner .
else
  echo "no .git directory in the context; history scan skipped"
fi`}).
		Sync(ctx)
	return err
}

// Scan Dockerfiles and other configuration for HIGH and CRITICAL misconfigurations
// +check
func (m *Chat) Config(ctx context.Context) error {
	_, err := dag.Container().
		From(trivyImage).
		WithMountedCache("/root/.cache/trivy", dag.CacheVolume("chat-trivy")).
		With(m.withSource).
		WithExec([]string{
			"trivy", "config",
			"--exit-code", "1",
			"--severity", "HIGH,CRITICAL",
			".",
		}).
		Sync(ctx)
	return err
}

// Build every image for amd64 and arm64, then fail on fixable critical CVEs
// in the amd64 SBOMs
// +check
func (m *Chat) Images(ctx context.Context) error {
	var jobs []func() error
	for _, component := range components {
		for _, platform := range platforms {
			jobs = append(jobs, func() error {
				image, err := m.Build(component, platform)
				if err != nil {
					return err
				}
				if _, err := image.Sync(ctx); err != nil {
					return fmt.Errorf("build %s for %s: %w", component, platform, err)
				}
				return nil
			})
		}
		jobs = append(jobs, func() error {
			if err := m.scan(ctx, component); err != nil {
				return fmt.Errorf("scan %s: %w", component, err)
			}
			return nil
		})
	}
	return parallel(jobs)
}

// Build one component image: chatserver, loadbalancer or web
func (m *Chat) Build(
	component string,
	// +optional
	// +default="linux/amd64"
	platform dagger.Platform,
) (*dagger.Container, error) {
	switch component {
	case "chatserver", "loadbalancer":
		return m.Source.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "build/Dockerfile",
			Target:     component,
			Platform:   platform,
		}), nil
	case "web":
		return m.Source.Directory("chat-app").DockerBuild(dagger.DirectoryDockerBuildOpts{
			Platform: platform,
		}), nil
	default:
		return nil, fmt.Errorf("unknown component %q: want one of %s", component, strings.Join(components, ", "))
	}
}

// Generate an SPDX JSON software bill of materials for one component image
func (m *Chat) Sbom(
	component string,
	// +optional
	// +default="linux/amd64"
	platform dagger.Platform,
) (*dagger.File, error) {
	image, err := m.Build(component, platform)
	if err != nil {
		return nil, err
	}
	return syft(image, "spdx-json", "none"), nil
}

// Publish every component for amd64 and arm64 and return one
// image:tag@digest reference per component, one per line
func (m *Chat) Publish(
	ctx context.Context,
	// Registry namespace, for example ghcr.io/golubovicluka
	registry string,
	username string,
	password *dagger.Secret,
	// Tags to push; the digest of the first one is returned
	tags []string,
	// Commit recorded in the org.opencontainers.image.revision label
	// +optional
	revision string,
) (string, error) {
	if len(tags) == 0 {
		return "", errors.New("at least one tag is required")
	}
	host, _, _ := strings.Cut(registry, "/")

	refs := make([]string, 0, len(components))
	for _, component := range components {
		variants := make([]*dagger.Container, 0, len(platforms))
		for _, platform := range platforms {
			image, err := m.Build(component, platform)
			if err != nil {
				return "", err
			}
			image = image.WithLabel("org.opencontainers.image.source", "https://github.com/golubovicluka/CS230-PZ-Distributed-chat-application")
			if revision != "" {
				image = image.WithLabel("org.opencontainers.image.revision", revision)
			}
			variants = append(variants, image)
		}

		var digestRef string
		for i, tag := range tags {
			address := fmt.Sprintf("%s/chat-%s:%s", registry, component, tag)
			ref, err := dag.Container().
				WithRegistryAuth(host, username, password).
				Publish(ctx, address, dagger.ContainerPublishOpts{PlatformVariants: variants})
			if err != nil {
				return "", fmt.Errorf("publish %s: %w", address, err)
			}
			if i == 0 {
				digestRef = ref
			}
		}
		refs = append(refs, digestRef)
	}
	return strings.Join(refs, "\n"), nil
}

// scan matches CVEs against a Syft-native SBOM that carries Go function
// symbols, so Go findings are limited to vulnerable functions the binary
// actually contains rather than every function of a module. Only fixable
// critical findings fail the check; everything else is reported.
func (m *Chat) scan(ctx context.Context, component string) error {
	image, err := m.Build(component, "linux/amd64")
	if err != nil {
		return err
	}
	_, err = dag.Container().
		From(grypeImage).
		WithEnvVariable("GRYPE_CHECK_FOR_APP_UPDATE", "false").
		WithMountedCache("/root/.cache/grype", dag.CacheVolume("chat-grype-db")).
		WithMountedFile("/sbom.json", syft(image, "syft-json", "all")).
		WithExec([]string{"/grype", "sbom:/sbom.json", "--output", "table"}).
		WithExec([]string{"/grype", "sbom:/sbom.json", "--only-fixed", "--fail-on", "critical", "--quiet"}).
		Sync(ctx)
	return err
}

func syft(image *dagger.Container, format, goSymbols string) *dagger.File {
	return dag.Container().
		From(syftImage).
		WithEnvVariable("SYFT_CHECK_FOR_APP_UPDATE", "false").
		WithEnvVariable("SYFT_GOLANG_CAPTURE_SYMBOLS", goSymbols).
		WithMountedFile("/image.tar", image.AsTarball()).
		WithExec([]string{"/syft", "oci-archive:/image.tar", "--output", format + "=/sbom.json"}).
		File("/sbom.json")
}

func (m *Chat) goContainer(image string) *dagger.Container {
	return dag.Container().
		From(image).
		WithEnvVariable("GOTOOLCHAIN", "local").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("chat-go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("chat-go-build")).
		WithDirectory("/src", m.Source, dagger.ContainerWithDirectoryOpts{
			Include: []string{"go.mod", "go.sum", "cmd/", "internal/", ".golangci.yml"},
		}).
		WithWorkdir("/src")
}

func (m *Chat) withSource(container *dagger.Container) *dagger.Container {
	return container.
		WithDirectory("/src", m.Source).
		WithWorkdir("/src")
}

func parallel(jobs []func() error) error {
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Go(func() { errs[i] = job() })
	}
	wg.Wait()
	return errors.Join(errs...)
}

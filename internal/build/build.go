package build

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/buildpacks/imgutil"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"

	"github.com/buildpacks/pack/internal/builder"
	"github.com/buildpacks/pack/internal/cache"
	"github.com/buildpacks/pack/internal/style"
	"github.com/buildpacks/pack/logging"
)

var (
	// SupportedPlatformAPIVersions lists the Platform API versions pack supports.
	SupportedPlatformAPIVersions = []string{"0.2", "0.3"}
)

type Builder interface {
	Name() string
	UID() int
	GID() int
	LifecycleDescriptor() builder.LifecycleDescriptor
	Image() imgutil.Image
}

type mountPaths struct {
	prefix string
}

func mountPathsForOS(os string) mountPaths {
	prefix := "/"
	if os == "windows" {
		prefix = `c:\`
	}
	return mountPaths{prefix: prefix}
}

func (m mountPaths) layersDir() string {
	return m.prefix + "layers"
}

func (m mountPaths) appDirName() string {
	return "workspace"
}

func (m mountPaths) appDir() string {
	return m.prefix + m.appDirName()
}

func (m mountPaths) cacheDir() string {
	return m.prefix + "cache"
}

func (m mountPaths) launchCacheDir() string {
	return m.prefix + "launch-cache"
}

func (m mountPaths) platformDir() string {
	return m.prefix + "platform"
}

type Lifecycle struct {
	builder            Builder
	logger             logging.Logger
	docker             client.CommonAPIClient
	appPath            string
	appOnce            *sync.Once
	httpProxy          string
	httpsProxy         string
	noProxy            string
	version            string
	platformAPIVersion string
	LayersVolume       string
	AppVolume          string
	Volumes            []string
	DefaultProcessType string
	fileFilter         func(string) bool
	os                 string
	mountPaths         mountPaths
}

type Cache interface {
	Name() string
	Clear(context.Context) error
}

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

func NewLifecycle(docker client.CommonAPIClient, logger logging.Logger) *Lifecycle {
	l := &Lifecycle{logger: logger, docker: docker}

	return l
}

type LifecycleOptions struct {
	AppPath            string
	Image              name.Reference
	Builder            Builder
	RunImage           string
	ClearCache         bool
	Publish            bool
	HTTPProxy          string
	HTTPSProxy         string
	NoProxy            string
	Network            string
	Volumes            []string
	DefaultProcessType string
	FileFilter         func(string) bool
}

func (l *Lifecycle) Execute(ctx context.Context, opts LifecycleOptions) error {
	err := l.Setup(opts)
	if err != nil {
		return err
	}
	defer l.Cleanup()

	buildCache := cache.NewVolumeCache(opts.Image, "build", l.docker)
	launchCache := cache.NewVolumeCache(opts.Image, "launch", l.docker)
	l.logger.Debugf("Using build cache volume %s", style.Symbol(buildCache.Name()))

	if opts.ClearCache {
		if err := buildCache.Clear(ctx); err != nil {
			return errors.Wrap(err, "clearing build cache")
		}
		l.logger.Debugf("Build cache %s cleared", style.Symbol(buildCache.Name()))
	}

	phaseFactory := NewDefaultPhaseFactory(l)

	l.logger.Info(style.Step("PREPARING"))
	if err := l.prepareAppVolume(ctx); err != nil {
		return err
	}

	l.logger.Info(style.Step("DETECTING"))
	if err := l.Detect(ctx, opts.Network, opts.Volumes, phaseFactory); err != nil {
		return err
	}

	l.logger.Info(style.Step("ANALYZING"))
	if err := l.Analyze(ctx, opts.Image.Name(), buildCache.Name(), opts.Publish, opts.ClearCache, phaseFactory); err != nil {
		return err
	}

	l.logger.Info(style.Step("RESTORING"))
	if opts.ClearCache {
		l.logger.Info("Skipping 'restore' due to clearing cache")
	} else if err := l.Restore(ctx, buildCache.Name(), phaseFactory); err != nil {
		return err
	}

	l.logger.Info(style.Step("BUILDING"))

	if err := l.Build(ctx, opts.Network, opts.Volumes, phaseFactory); err != nil {
		return err
	}

	l.logger.Info(style.Step("EXPORTING"))
	if err := l.Export(ctx, opts.Image.Name(), opts.RunImage, opts.Publish, launchCache.Name(), buildCache.Name(), phaseFactory); err != nil {
		return err
	}

	return nil
}

func (l *Lifecycle) Setup(opts LifecycleOptions) error {
	l.LayersVolume = "pack-layers-" + randString(10)
	l.AppVolume = "pack-app-" + randString(10)
	l.appPath = opts.AppPath
	l.appOnce = &sync.Once{}
	l.builder = opts.Builder
	l.httpProxy = opts.HTTPProxy
	l.httpsProxy = opts.HTTPSProxy
	l.noProxy = opts.NoProxy
	l.version = opts.Builder.LifecycleDescriptor().Info.Version.String()
	l.platformAPIVersion = opts.Builder.LifecycleDescriptor().API.PlatformVersion.String()
	l.DefaultProcessType = opts.DefaultProcessType
	l.fileFilter = opts.FileFilter

	os, err := l.builder.Image().OS()
	if err != nil {
		return err
	}
	l.os = os
	l.mountPaths = mountPathsForOS(l.os)
	return nil
}

func (l *Lifecycle) Cleanup() error {
	var reterr error
	if err := l.docker.VolumeRemove(context.Background(), l.LayersVolume, true); err != nil {
		reterr = errors.Wrapf(err, "failed to clean up layers volume %s", l.LayersVolume)
	}
	if err := l.docker.VolumeRemove(context.Background(), l.AppVolume, true); err != nil {
		reterr = errors.Wrapf(err, "failed to clean up app volume %s", l.AppVolume)
	}
	return reterr
}

func randString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(rand.Intn(26))
	}
	return string(b)
}

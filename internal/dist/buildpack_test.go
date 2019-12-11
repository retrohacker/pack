package dist_test

import (
	"io"
	// "io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/heroku/color"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"

	"github.com/buildpack/pack/internal/archive"
	// "github.com/buildpack/pack/internal/archive"
	"github.com/buildpack/pack/internal/blob"
	"github.com/buildpack/pack/internal/dist"
	h "github.com/buildpack/pack/testhelpers"
)

func TestBuildpack(t *testing.T) {
	color.Disable(true)
	defer color.Disable(false)
	spec.Run(t, "buildpack", testBuildpack, spec.Parallel(), spec.Report(report.Terminal{}))
}

func testBuildpack(t *testing.T, when spec.G, it spec.S) {
	var tmpBpDir string

	it.Before(func() {
		var err error
		tmpBpDir, err = ioutil.TempDir("", "")
		h.AssertNil(t, err)
	})

	it.After(func() {
		h.AssertNil(t, os.RemoveAll(tmpBpDir))
	})

	var writeBlobToFile = func(bp dist.Buildpack) string {
		t.Helper()

		bpReader, err := bp.Open()
		h.AssertNil(t, err)
		defer bpReader.Close()

		tmpDir, err := ioutil.TempDir("", "")
		h.AssertNil(t, err)

		path := filepath.Join(tmpDir, "bp.tar")
		bpWriter, err := os.Create(path)
		h.AssertNil(t, err)

		_, err = io.Copy(bpWriter, bpReader)
		h.AssertNil(t, err)

		return path
	}

	when("#BuildpackFromRootBlob", func() {
		it("parses the descriptor file", func() {
			h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "buildpack.toml"), []byte(`
api = "0.3"

[buildpack]
id = "bp.one"
version = "1.2.3"

[[stacks]]
id = "some.stack.id"
`), os.ModePerm))

			bp, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
			h.AssertNil(t, err)

			h.AssertEq(t, bp.Descriptor().API.String(), "0.3")
			h.AssertEq(t, bp.Descriptor().Info.ID, "bp.one")
			h.AssertEq(t, bp.Descriptor().Info.Version, "1.2.3")
			h.AssertEq(t, bp.Descriptor().Stacks[0].ID, "some.stack.id")
		})

		it("translates blob to distribution format", func() {
			h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "buildpack.toml"), []byte(`
api = "0.3"

[buildpack]
id = "bp.one"
version = "1.2.3"

[[stacks]]
id = "some.stack.id"
`), os.ModePerm))
			h.AssertNil(t, os.MkdirAll(filepath.Join(tmpBpDir, "bin"), 0700))
			h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "bin", "detect"), []byte("detect-contents"), 0700))
			h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "bin", "build"), []byte("build-contents"), 0700))

			bp, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
			h.AssertNil(t, err)

			tarPath := writeBlobToFile(bp)
			defer os.Remove(tarPath)

			h.AssertOnTarEntry(t, tarPath,
				"/cnb/buildpacks/bp.one",
				h.IsDirectory(),
				h.HasFileMode(0755),
				h.HasModTime(archive.NormalizedDateTime),
			)

			h.AssertOnTarEntry(t, tarPath,
				"/cnb/buildpacks/bp.one/1.2.3",
				h.IsDirectory(),
				h.HasFileMode(0755),
				h.HasModTime(archive.NormalizedDateTime),
			)

			h.AssertOnTarEntry(t, tarPath,
				"/cnb/buildpacks/bp.one/1.2.3/bin",
				h.IsDirectory(),
				h.HasFileMode(0700),
				h.HasModTime(archive.NormalizedDateTime),
			)

			h.AssertOnTarEntry(t, tarPath,
				"/cnb/buildpacks/bp.one/1.2.3/bin/detect",
				h.HasFileMode(0700),
				h.HasModTime(archive.NormalizedDateTime),
				h.ContentEquals("detect-contents"),
			)

			h.AssertOnTarEntry(t, tarPath,
				"/cnb/buildpacks/bp.one/1.2.3/bin/build",
				h.HasFileMode(0700),
				h.HasModTime(archive.NormalizedDateTime),
				h.ContentEquals("build-contents"),
			)
		})

		when("there is no descriptor file", func() {
			it("returns error", func() {
				_, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
				h.AssertError(t, err, "could not find entry path 'buildpack.toml'")
			})
		})

		when("there is no api field", func() {
			it.Before(func() {
				h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "buildpack.toml"), []byte(`
[buildpack]
id = "bp.one"
version = "1.2.3"

[[stacks]]
id = "some.stack.id"
`), os.ModePerm))
			})

			it("assumes an api version", func() {
				bp, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
				h.AssertNil(t, err)
				h.AssertEq(t, bp.Descriptor().API.String(), "0.1")
			})
		})

		when("there is no id", func() {
			it.Before(func() {
				h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "buildpack.toml"), []byte(`
[buildpack]
id = ""
version = "1.2.3"

[[stacks]]
id = "some.stack.id"
`), os.ModePerm))
			})

			it("returns error", func() {
				_, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
				h.AssertError(t, err, "'buildpack.id' is required")
			})
		})

		when("there is no version", func() {
			it.Before(func() {
				h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "buildpack.toml"), []byte(`
[buildpack]
id = "bp.one"
version = ""

[[stacks]]
id = "some.stack.id"
`), os.ModePerm))
			})

			it("returns error", func() {
				_, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
				h.AssertError(t, err, "'buildpack.version' is required")
			})
		})

		when("both stacks and order are present", func() {
			it.Before(func() {
				h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "buildpack.toml"), []byte(`
[buildpack]
id = "bp.one"
version = "1.2.3"

[[stacks]]
id = "some.stack.id"

[[order]]
[[order.group]]
  id = "bp.nested"
  version = "bp.nested.version"
`), os.ModePerm))
			})

			it("returns error", func() {
				_, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
				h.AssertError(t, err, "cannot have both 'stacks' and an 'order' defined")
			})
		})

		when("missing stacks and order", func() {
			it.Before(func() {
				h.AssertNil(t, ioutil.WriteFile(filepath.Join(tmpBpDir, "buildpack.toml"), []byte(`
[buildpack]
id = "bp.one"
version = "1.2.3"
`), os.ModePerm))
			})

			it("returns error", func() {
				_, err := dist.BuildpackFromRootBlob(blob.NewBlob(tmpBpDir))
				h.AssertError(t, err, "must have either 'stacks' or an 'order' defined")
			})
		})
	})
}
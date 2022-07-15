/// Copyright 2019 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/api/ifc"
	"sigs.k8s.io/kustomize/api/internal/git"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

type testData struct {
	path            string
	expectedContent string
}

var testCases = []testData{
	{
		path:            "foo/project/fileA.yaml",
		expectedContent: "fileA content",
	},
	{
		path:            "foo/project/subdir1/fileB.yaml",
		expectedContent: "fileB content",
	},
	{
		path:            "foo/project/subdir2/fileC.yaml",
		expectedContent: "fileC content",
	},
	{
		path:            "foo/project/fileD.yaml",
		expectedContent: "fileD content",
	},
}

func MakeFakeFs(td []testData) filesys.FileSystem {
	fSys := filesys.MakeFsInMemory()
	for _, x := range td {
		fSys.WriteFile(x.path, []byte(x.expectedContent))
	}
	return fSys
}

func makeLoader() *fileLoader {
	return NewFileLoaderAtRoot(MakeFakeFs(testCases))
}

func TestLoaderLoad(t *testing.T) {
	req := require.New(t)

	l1 := makeLoader()
	req.Equal("/", l1.Root())

	for _, x := range testCases {
		b, err := l1.Load(x.path)
		req.NoError(err)
		req.Equal([]byte(x.expectedContent), b)
	}
	l2, err := l1.New("foo/project")
	req.NoError(err)
	req.Equal("/foo/project", l2.Root())

	for _, x := range testCases {
		b, err := l2.Load(strings.TrimPrefix(x.path, "foo/project/"))
		req.NoError(err)
		req.Equal([]byte(x.expectedContent), b)
	}
	l2, err = l1.New("foo/project/") // Assure trailing slash stripped
	req.NoError(err)
	req.Equal("/foo/project", l2.Root())
}

func TestLoaderNewSubDir(t *testing.T) {
	req := require.New(t)

	l1, err := makeLoader().New("foo/project")
	req.NoError(err)

	l2, err := l1.New("subdir1")
	req.NoError(err)
	req.Equal("/foo/project/subdir1", l2.Root())

	x := testCases[1]
	b, err := l2.Load("fileB.yaml")
	req.NoError(err)
	req.Equal([]byte(x.expectedContent), b)
}

func TestLoaderBadRelative(t *testing.T) {
	req := require.New(t)

	l1, err := makeLoader().New("foo/project/subdir1")
	req.NoError(err)
	req.Equal("/foo/project/subdir1", l1.Root())

	// Cannot cd into a file.
	l2, err := l1.New("fileB.yaml")
	req.Error(err)

	// It's not okay to stay at the same place.
	l2, err = l1.New(filesys.SelfDir)
	req.Error(err)

	// It's not okay to go up and back down into same place.
	l2, err = l1.New("../subdir1")
	req.Error(err)

	// It's not okay to go up via a relative path.
	l2, err = l1.New("..")
	req.Error(err)

	// It's not okay to go up via an absolute path.
	l2, err = l1.New("/foo/project")
	req.Error(err)

	// It's not okay to go to the root.
	l2, err = l1.New("/")
	req.Error(err)

	// It's okay to go up and down to a sibling.
	l2, err = l1.New("../subdir2")
	req.NoError(err)
	req.Equal("/foo/project/subdir2", l2.Root())

	x := testCases[2]
	b, err := l2.Load("fileC.yaml")
	req.NoError(err)
	req.Equal([]byte(x.expectedContent), b)

	// It's not OK to go over to a previously visited directory.
	// Must disallow going back and forth in a cycle.
	l1, err = l2.New("../subdir1")
	req.Error(err)
}

func TestLoaderMisc(t *testing.T) {
	l := makeLoader()
	_, err := l.New("")
	require.Error(t, err)

	// important that url doesn't have repo; otherwise need to
	// clean tmp directory created
	_, err = l.New("https://google.com/project")
	require.Error(t, err)
}

const (
	contentOk           = "hi there, i'm OK data"
	contentExteriorData = "i am data from outside the root"
)

// Create a structure like this
//
//   /tmp/kustomize-test-random
//   ├── base
//   │   ├── okayData
//   │   ├── symLinkToOkayData -> okayData
//   │   └── symLinkToExteriorData -> ../exteriorData
//   └── exteriorData
//
func commonSetupForLoaderRestrictionTest(t *testing.T) (string, filesys.FileSystem) {
	t.Helper()
	dir := t.TempDir()
	fSys := filesys.MakeFsOnDisk()
	fSys.Mkdir(filepath.Join(dir, "base"))

	fSys.WriteFile(
		filepath.Join(dir, "base", "okayData"), []byte(contentOk))

	fSys.WriteFile(
		filepath.Join(dir, "exteriorData"), []byte(contentExteriorData))

	os.Symlink(
		filepath.Join(dir, "base", "okayData"),
		filepath.Join(dir, "base", "symLinkToOkayData"))
	os.Symlink(
		filepath.Join(dir, "exteriorData"),
		filepath.Join(dir, "base", "symLinkToExteriorData"))
	return dir, fSys
}

// Make sure everything works when loading files
// in or below the loader root.
func doSanityChecksAndDropIntoBase(
	t *testing.T, l ifc.Loader) ifc.Loader {
	t.Helper()
	req := require.New(t)

	data, err := l.Load(path.Join("base", "okayData"))
	req.NoError(err)
	req.Equal(contentOk, string(data))

	data, err = l.Load("exteriorData")
	req.NoError(err)
	req.Equal(contentExteriorData, string(data))

	// Drop in.
	l, err = l.New("base")
	req.NoError(err)

	// Reading okayData works.
	data, err = l.Load("okayData")
	req.NoError(err)
	req.Equal(contentOk, string(data))

	// Reading local symlink to okayData works.
	data, err = l.Load("symLinkToOkayData")
	req.NoError(err)
	req.Equal(contentOk, string(data))

	return l
}

func TestRestrictionRootOnlyInRealLoader(t *testing.T) {
	req := require.New(t)
	dir, fSys := commonSetupForLoaderRestrictionTest(t)

	var l ifc.Loader

	l = newLoaderOrDie(RestrictionRootOnly, fSys, dir)

	l = doSanityChecksAndDropIntoBase(t, l)

	// Reading symlink to exteriorData fails.
	_, err := l.Load("symLinkToExteriorData")
	req.Error(err)
	req.Contains(err.Error(), "is not in or below")

	// Attempt to read "up" fails, though earlier we were
	// able to read this file when root was "..".
	_, err = l.Load("../exteriorData")
	req.Error(err)
	req.Contains(err.Error(), "is not in or below")
}

func TestRestrictionNoneInRealLoader(t *testing.T) {
	dir, fSys := commonSetupForLoaderRestrictionTest(t)

	var l ifc.Loader

	l = newLoaderOrDie(RestrictionNone, fSys, dir)

	l = doSanityChecksAndDropIntoBase(t, l)

	// Reading symlink to exteriorData works.
	_, err := l.Load("symLinkToExteriorData")
	require.NoError(t, err)

	// Attempt to read "up" works.
	_, err = l.Load("../exteriorData")
	require.NoError(t, err)
}

func TestNewLoaderAtGitClone(t *testing.T) {
	req := require.New(t)

	rootURL := "github.com/someOrg/someRepo"
	pathInRepo := "foo/base"
	url := rootURL + "/" + pathInRepo
	coRoot := "/tmp"
	fSys := filesys.MakeFsInMemory()
	fSys.MkdirAll(coRoot + "/" + pathInRepo)
	fSys.WriteFile(
		coRoot+"/"+pathInRepo+"/"+
			konfig.DefaultKustomizationFileName(),
		[]byte(`
whatever
`))

	repoSpec, err := git.NewRepoSpecFromURL(url)
	req.NoError(err)

	l, err := newLoaderAtGitClone(
		repoSpec, fSys, nil,
		git.DoNothingCloner(filesys.ConfirmedDir(coRoot)))
	req.NoError(err)
	req.Equal(coRoot+"/"+pathInRepo, l.Root())

	// cycles
	_, err = l.New(url)
	req.Error(err)

	_, err = l.New(rootURL + "/" + "foo")
	req.Error(err)

	// not cycle: new url does not contain loaded url
	pathInRepo = "foo/overlay"
	fSys.MkdirAll(coRoot + "/" + pathInRepo)
	url = rootURL + "/" + pathInRepo
	l2, err := l.New(url)
	req.NoError(err)
	req.Equal(coRoot+"/"+pathInRepo, l2.Root())
}

func TestLoaderDisallowsLocalBaseFromRemoteOverlay(t *testing.T) {
	req := require.New(t)

	// Define an overlay-base structure in the file system.
	topDir := "/whatever"
	cloneRoot := topDir + "/someClone"
	fSys := filesys.MakeFsInMemory()
	fSys.MkdirAll(topDir + "/highBase")
	fSys.MkdirAll(cloneRoot + "/foo/base")
	fSys.MkdirAll(cloneRoot + "/foo/overlay")

	var l1 ifc.Loader

	// Establish that a local overlay can navigate
	// to the local bases.
	l1 = newLoaderOrDie(
		RestrictionRootOnly, fSys, cloneRoot+"/foo/overlay")
	req.Equal(cloneRoot+"/foo/overlay", l1.Root())

	l2, err := l1.New("../base")
	req.NoError(err)
	req.Equal(cloneRoot+"/foo/base", l2.Root())

	l3, err := l2.New("../../../highBase")
	req.NoError(err)
	req.Equal(topDir+"/highBase", l3.Root())

	// Establish that a Kustomization found in cloned
	// repo can reach (non-remote) bases inside the clone
	// but cannot reach a (non-remote) base outside the
	// clone but legitimately on the local file system.
	// This is to avoid a surprising interaction between
	// a remote K and local files.  The remote K would be
	// non-functional on its own since by definition it
	// would refer to a non-remote base file that didn't
	// exist in its own repository, so presumably the
	// remote K would be deliberately designed to phish
	// for local K's.
	repoSpec, err := git.NewRepoSpecFromURL(
		"github.com/someOrg/someRepo/foo/overlay")
	req.NoError(err)

	l1, err = newLoaderAtGitClone(
		repoSpec, fSys, nil,
		git.DoNothingCloner(filesys.ConfirmedDir(cloneRoot)))
	req.NoError(err)
	req.Equal(cloneRoot+"/foo/overlay", l1.Root())

	// This is okay.
	l2, err = l1.New("../base")
	req.NoError(err)
	req.Equal(cloneRoot+"/foo/base", l2.Root())

	// This is not okay.
	_, err = l2.New("../../../highBase")
	req.Error(err)
	req.Contains(err.Error(),
		"base '/whatever/highBase' is outside '/whatever/someClone'")
}

func TestLocalLoaderReferencingGitBase(t *testing.T) {
	req := require.New(t)

	topDir := "/whatever"
	cloneRoot := topDir + "/someClone"
	fSys := filesys.MakeFsInMemory()
	fSys.MkdirAll(cloneRoot + "/foo/base")

	l1 := newLoaderAtConfirmedDir(
		RestrictionRootOnly, filesys.ConfirmedDir(topDir), fSys, nil,
		git.DoNothingCloner(filesys.ConfirmedDir(cloneRoot)))
	req.Equal(topDir, l1.Root())

	l2, err := l1.New("github.com/someOrg/someRepo/foo/base")
	req.NoError(err)
	req.Equal(cloneRoot+"/foo/base", l2.Root())
}

func TestRepoDirectCycleDetection(t *testing.T) {
	req := require.New(t)

	topDir := "/cycles"
	cloneRoot := topDir + "/someClone"
	fSys := filesys.MakeFsInMemory()
	err := fSys.MkdirAll(cloneRoot + "/foo")
	req.NoError(err)

	p1 := "github.com/someOrg/someRepo/foo"
	rs1, err := git.NewRepoSpecFromURL(p1)
	req.NoError(err)

	l1, err := newLoaderAtGitClone(
		rs1, fSys, nil, git.DoNothingCloner(filesys.ConfirmedDir(cloneRoot)))
	req.NoError(err)
	req.Equal(cloneRoot+"/foo", l1.Root())

	_, err = l1.New(p1)
	req.Error(err)
	req.Contains(err.Error(), "cycle detected")
}

func TestRepoIndirectCycleDetection(t *testing.T) {
	req := require.New(t)

	topDir := "/cycles"
	cloneRoot := topDir + "/someClone"
	fSys := filesys.MakeFsInMemory()
	fSys.MkdirAll(cloneRoot)

	l0 := newLoaderAtConfirmedDir(
		RestrictionRootOnly, filesys.ConfirmedDir(topDir), fSys, nil,
		git.DoNothingCloner(filesys.ConfirmedDir(cloneRoot)))

	p1 := "github.com/someOrg/someRepo1"
	p2 := "github.com/someOrg/someRepo2"

	l1, err := l0.New(p1)
	req.NoError(err)

	l2, err := l1.New(p2)
	req.NoError(err)

	_, err = l2.New(p1)
	req.Error(err)
	req.Contains(err.Error(), "cycle detected")
}

// Inspired by https://hassansin.github.io/Unit-Testing-http-client-in-Go
type fakeRoundTripper func(req *http.Request) *http.Response

func (f fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

func makeFakeHTTPClient(fn fakeRoundTripper) *http.Client {
	return &http.Client{
		Transport: fn,
	}
}

// TestLoaderHTTP test http file loader
func TestLoaderHTTP(t *testing.T) {
	req := require.New(t)

	var testCasesFile = []testData{
		{
			path:            "http/file.yaml",
			expectedContent: "file content",
		},
	}

	l1 := NewFileLoaderAtRoot(MakeFakeFs(testCasesFile))
	req.Equal("/", l1.Root())

	for _, x := range testCasesFile {
		b, err := l1.Load(x.path)
		req.NoError(err)
		req.Equal([]byte(x.expectedContent), b)
	}

	var testCasesHTTP = []testData{
		{
			path:            "http://example.com/resource.yaml",
			expectedContent: "http content",
		},
		{
			path:            "https://example.com/resource.yaml",
			expectedContent: "https content",
		},
	}

	for _, x := range testCasesHTTP {
		hc := makeFakeHTTPClient(func(request *http.Request) *http.Response {
			u := request.URL.String()
			req.Equal(x.path, u)
			return &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewBufferString(x.expectedContent)),
				Header:     make(http.Header),
			}
		})
		l2 := l1
		l2.http = hc
		b, err := l2.Load(x.path)
		req.NoError(err)
		req.Equal([]byte(x.expectedContent), b)
	}

	var testCaseUnsupported = []testData{
		{
			path:            "httpsnotreal://example.com/resource.yaml",
			expectedContent: "invalid",
		},
	}
	for _, x := range testCaseUnsupported {
		hc := makeFakeHTTPClient(func(req *http.Request) *http.Response {
			t.Fatalf("unexpected request to URL %s", req.URL.String())
			return nil
		})
		l2 := l1
		l2.http = hc
		_, err := l2.Load(x.path)
		req.Error(err)
	}
}

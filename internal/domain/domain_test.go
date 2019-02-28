package domain

import (
	"compress/gzip"
	"io/ioutil"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-pages/internal/client"
	"gitlab.com/gitlab-org/gitlab-pages/internal/fixture"
)

func serveFileOrNotFound(domain *D) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !domain.ServeFileHTTP(w, r) {
			domain.ServeNotFoundHTTP(w, r)
		}
	}
}

func assertRedirectTo(t *testing.T, h http.HandlerFunc, method string, url string, values url.Values, expectedURL string) {
	assert.HTTPRedirect(t, h, method, url, values)
	assert.HTTPBodyContains(t, h, method, url, values, `<a href="//`+expectedURL+`">Found</a>`)
}

func testGroupServeHTTPHost(t *testing.T, host string) {
	testGroup := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/group.test.io/", Path: "group/group.test.io/public/"},
				{Prefix: "/group.gitlab-example.com/", Path: "group/group.gitlab-example.com/public/"},
				{Prefix: "/project/", Path: "group/project/public/"},
				{Prefix: "/project2/", Path: "group/project2/public/"},
			},
		},
	}

	makeURL := func(path string) string {
		return "http://" + host + path
	}

	serve := serveFileOrNotFound(testGroup)

	assert.HTTPBodyContains(t, serve, "GET", makeURL("/"), nil, "main-dir")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/index"), nil, "main-dir")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/index.html"), nil, "main-dir")
	assertRedirectTo(t, serve, "GET", makeURL("/project"), nil, host+"/project/")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project/"), nil, "project-subdir")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project/index"), nil, "project-subdir")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project/index/"), nil, "project-subdir")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project/index.html"), nil, "project-subdir")
	assertRedirectTo(t, serve, "GET", makeURL("/project/subdir"), nil, host+"/project/subdir/")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project/subdir/"), nil, "project-subsubdir")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project2/"), nil, "project2-main")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project2/index"), nil, "project2-main")
	assert.HTTPBodyContains(t, serve, "GET", makeURL("/project2/index.html"), nil, "project2-main")
	assert.HTTPRedirect(t, serve, "GET", makeURL("/private.project/"), nil)
	assert.HTTPError(t, serve, "GET", makeURL("//about.gitlab.com/%2e%2e"), nil)
	assert.HTTPError(t, serve, "GET", makeURL("/symlink"), nil)
	assert.HTTPError(t, serve, "GET", makeURL("/symlink/index.html"), nil)
	assert.HTTPError(t, serve, "GET", makeURL("/symlink/subdir/"), nil)
	assert.HTTPError(t, serve, "GET", makeURL("/project/fifo"), nil)
	assert.HTTPError(t, serve, "GET", makeURL("/not-existing-file"), nil)
	assert.HTTPError(t, serve, "GET", makeURL("/project//about.gitlab.com/%2e%2e"), nil)
}

func TestGroupServeHTTP(t *testing.T) {
	cleanup := setUpTests(t)
	defer cleanup()

	t.Run("group.test.io", func(t *testing.T) { testGroupServeHTTPHost(t, "group.test.io") })
	t.Run("group.test.io:8080", func(t *testing.T) { testGroupServeHTTPHost(t, "group.test.io:8080") })
}

func TestDomainServeHTTP(t *testing.T) {
	cleanup := setUpTests(t)
	defer cleanup()

	testDomain := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/", Path: "group/project2/public/"},
			},
		},
	}

	assert.HTTPBodyContains(t, serveFileOrNotFound(testDomain), "GET", "/", nil, "project2-main")
	assert.HTTPBodyContains(t, serveFileOrNotFound(testDomain), "GET", "/index.html", nil, "project2-main")
	assert.HTTPRedirect(t, serveFileOrNotFound(testDomain), "GET", "/subdir", nil)
	assert.HTTPBodyContains(t, serveFileOrNotFound(testDomain), "GET", "/subdir", nil,
		`<a href="/subdir/">Found</a>`)
	assert.HTTPBodyContains(t, serveFileOrNotFound(testDomain), "GET", "/subdir/", nil, "project2-subdir")
	assert.HTTPBodyContains(t, serveFileOrNotFound(testDomain), "GET", "/subdir/index.html", nil, "project2-subdir")
	assert.HTTPError(t, serveFileOrNotFound(testDomain), "GET", "//about.gitlab.com/%2e%2e", nil)
	assert.HTTPError(t, serveFileOrNotFound(testDomain), "GET", "/not-existing-file", nil)
}

func TestIsHTTPSOnly(t *testing.T) {
	tests := []struct {
		name     string
		domain   *D
		url      string
		expected bool
	}{
		{
			name: "Custom domain with HTTPS-only enabled",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/", Path: "group/project/public/", HTTPSOnly: true},
					},
				},
			},
			url:      "http://custom-domain",
			expected: true,
		},
		{
			name: "Custom domain with HTTPS-only disabled",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/", Path: "group/project/public/", HTTPSOnly: false},
					},
				},
			},
			url:      "http://custom-domain",
			expected: false,
		},
		{
			name: "Default group domain with HTTPS-only enabled",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/test-domain/", Path: "group/test-domain/public/", HTTPSOnly: true},
					},
				},
			},
			url:      "http://test-domain",
			expected: true,
		},
		{
			name: "Default group domain with HTTPS-only disabled",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/test-domain/", Path: "group/test-domain/public/", HTTPSOnly: false},
					},
				},
			},
			url:      "http://test-domain",
			expected: false,
		},
		{
			name: "Case-insensitive default group domain with HTTPS-only enabled",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/test-domain/", Path: "group/test-domain/public/", HTTPSOnly: true},
					},
				},
			},
			url:      "http://Test-domain",
			expected: true,
		},
		{
			name: "Other group domain with HTTPS-only enabled",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/project/", Path: "group/project/public/", HTTPSOnly: true},
					},
				},
			},
			url:      "http://test-domain/project",
			expected: true,
		},
		{
			name: "Other group domain with HTTPS-only disabled",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/project/", Path: "group/project/public/", HTTPSOnly: false},
					},
				},
			},
			url:      "http://test-domain/project",
			expected: false,
		},
		{
			name: "Unknown project",
			domain: &D{
				DomainResponse: &client.DomainResponse{
					LookupPath: []client.LookupPath{
						{Prefix: "/project/", Path: "group/project/public/"},
					},
				},
			},
			url:      "http://test-domain/project",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, test.url, nil)
			assert.Equal(t, test.domain.IsHTTPSOnly(req), test.expected)
		})
	}
}

func testHTTPGzip(t *testing.T, handler http.HandlerFunc, mode, url string, values url.Values, acceptEncoding string, str interface{}, contentType string, ungzip bool) {
	w := httptest.NewRecorder()
	req, err := http.NewRequest(mode, url+"?"+values.Encode(), nil)
	require.NoError(t, err)
	if acceptEncoding != "" {
		req.Header.Add("Accept-Encoding", acceptEncoding)
	}
	handler(w, req)

	if ungzip {
		reader, err := gzip.NewReader(w.Body)
		require.NoError(t, err)
		defer reader.Close()

		contentEncoding := w.Header().Get("Content-Encoding")
		assert.Equal(t, "gzip", contentEncoding, "Content-Encoding")

		bytes, err := ioutil.ReadAll(reader)
		require.NoError(t, err)
		assert.Contains(t, string(bytes), str)
	} else {
		assert.Contains(t, w.Body.String(), str)
	}

	assert.Equal(t, contentType, w.Header().Get("Content-Type"))
}

func TestGroupServeHTTPGzip(t *testing.T) {
	cleanup := setUpTests(t)
	defer cleanup()

	testGroup := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/group.test.io/", Path: "group/group.test.io/public/"},
				{Prefix: "/group.gitlab-example.com/", Path: "group/group.gitlab-example.com/public/"},
				{Prefix: "/project/", Path: "group/project/public/"},
				{Prefix: "/project2/", Path: "group/project2/public/"},
			},
		},
	}

	testSet := []struct {
		mode           string      // HTTP mode
		url            string      // Test URL
		acceptEncoding string      // Accept encoding header
		body           interface{} // Expected body at above URL
		contentType    string      // Expected content-type
		ungzip         bool        // Expect the response to be gzipped?
	}{
		// No gzip encoding requested
		{"GET", "/index.html", "", "main-dir", "text/html; charset=utf-8", false},
		{"GET", "/index.html", "identity", "main-dir", "text/html; charset=utf-8", false},
		{"GET", "/index.html", "gzip; q=0", "main-dir", "text/html; charset=utf-8", false},
		// gzip encoding requested,
		{"GET", "/index.html", "*", "main-dir", "text/html; charset=utf-8", true},
		{"GET", "/index.html", "identity, gzip", "main-dir", "text/html; charset=utf-8", true},
		{"GET", "/index.html", "gzip", "main-dir", "text/html; charset=utf-8", true},
		{"GET", "/index.html", "gzip; q=1", "main-dir", "text/html; charset=utf-8", true},
		{"GET", "/index.html", "gzip; q=0.9", "main-dir", "text/html; charset=utf-8", true},
		{"GET", "/index.html", "gzip, deflate", "main-dir", "text/html; charset=utf-8", true},
		{"GET", "/index.html", "gzip; q=1, deflate", "main-dir", "text/html; charset=utf-8", true},
		{"GET", "/index.html", "gzip; q=0.9, deflate", "main-dir", "text/html; charset=utf-8", true},
		// gzip encoding requested, but url does not have compressed content on disk
		{"GET", "/project2/index.html", "*", "project2-main", "text/html; charset=utf-8", false},
		{"GET", "/project2/index.html", "identity, gzip", "project2-main", "text/html; charset=utf-8", false},
		{"GET", "/project2/index.html", "gzip", "project2-main", "text/html; charset=utf-8", false},
		{"GET", "/project2/index.html", "gzip; q=1", "project2-main", "text/html; charset=utf-8", false},
		{"GET", "/project2/index.html", "gzip; q=0.9", "project2-main", "text/html; charset=utf-8", false},
		{"GET", "/project2/index.html", "gzip, deflate", "project2-main", "text/html; charset=utf-8", false},
		{"GET", "/project2/index.html", "gzip; q=1, deflate", "project2-main", "text/html; charset=utf-8", false},
		{"GET", "/project2/index.html", "gzip; q=0.9, deflate", "project2-main", "text/html; charset=utf-8", false},
		// malformed headers
		{"GET", "/index.html", ";; gzip", "main-dir", "text/html; charset=utf-8", false},
		{"GET", "/index.html", "middle-out", "main-dir", "text/html; charset=utf-8", false},
		{"GET", "/index.html", "gzip; quality=1", "main-dir", "text/html; charset=utf-8", false},
		// Symlinked .gz files are not supported
		{"GET", "/gz-symlink", "*", "data", "text/plain; charset=utf-8", false},
		// Unknown file-extension, with text content
		{"GET", "/text.unknown", "*", "hello", "text/plain; charset=utf-8", true},
		{"GET", "/text-nogzip.unknown", "*", "hello", "text/plain; charset=utf-8", false},
		// Unknown file-extension, with PNG content
		{"GET", "/image.unknown", "*", "GIF89a", "image/gif", true},
		{"GET", "/image-nogzip.unknown", "*", "GIF89a", "image/gif", false},
	}

	for _, tt := range testSet {
		URL := "http://group.test.io" + tt.url
		testHTTPGzip(t, serveFileOrNotFound(testGroup), tt.mode, URL, nil, tt.acceptEncoding, tt.body, tt.contentType, tt.ungzip)
	}
}

func testHTTP404(t *testing.T, handler http.HandlerFunc, mode, url string, values url.Values, str interface{}) {
	w := httptest.NewRecorder()
	req, err := http.NewRequest(mode, url+"?"+values.Encode(), nil)
	require.NoError(t, err)
	handler(w, req)

	contentType, _, _ := mime.ParseMediaType(w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusNotFound, w.Code, "HTTP status")
	assert.Equal(t, "text/html", contentType, "Content-Type")
	assert.Contains(t, w.Body.String(), str)
}

func TestGroup404ServeHTTP(t *testing.T) {
	cleanup := setUpTests(t)
	defer cleanup()

	testGroup := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/domain.404/", Path: "group.404/domain.404/public/"},
				{Prefix: "/group.404.test.io/", Path: "group.404/group.404.test.io/public/"},
				{Prefix: "/project.404/", Path: "group.404/project.404/public/"},
				{Prefix: "/project.no.404/", Path: "group.404/project.no.404/public/"},
			},
		},
	}

	testHTTP404(t, serveFileOrNotFound(testGroup), "GET", "http://group.404.test.io/project.404/not/existing-file", nil, "Custom 404 project page")
	testHTTP404(t, serveFileOrNotFound(testGroup), "GET", "http://group.404.test.io/project.404/", nil, "Custom 404 project page")
	testHTTP404(t, serveFileOrNotFound(testGroup), "GET", "http://group.404.test.io/not/existing-file", nil, "Custom 404 group page")
	testHTTP404(t, serveFileOrNotFound(testGroup), "GET", "http://group.404.test.io/not-existing-file", nil, "Custom 404 group page")
	testHTTP404(t, serveFileOrNotFound(testGroup), "GET", "http://group.404.test.io/", nil, "Custom 404 group page")
	assert.HTTPBodyNotContains(t, serveFileOrNotFound(testGroup), "GET", "http://group.404.test.io/project.404.symlink/not/existing-file", nil, "Custom 404 project page")

	// Ensure the namespace project's custom 404.html is not used by projects
	testHTTP404(t, serveFileOrNotFound(testGroup), "GET", "http://group.404.test.io/project.no.404/not/existing-file", nil, "The page you're looking for could not be found.")
}

func TestDomain404ServeHTTP(t *testing.T) {
	cleanup := setUpTests(t)
	defer cleanup()

	testDomain := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/", Path: "group.404/domain.404/public/"},
			},
		},
	}

	testHTTP404(t, serveFileOrNotFound(testDomain), "GET", "http://group.404.test.io/not-existing-file", nil, "Custom 404 group page")
	testHTTP404(t, serveFileOrNotFound(testDomain), "GET", "http://group.404.test.io/", nil, "Custom 404 group page")
}

func TestPredefined404ServeHTTP(t *testing.T) {
	cleanup := setUpTests(t)
	defer cleanup()

	testDomain := &D{
		DomainResponse: &client.DomainResponse{},
	}

	testHTTP404(t, serveFileOrNotFound(testDomain), "GET", "http://group.test.io/not-existing-file", nil, "The page you're looking for could not be found")
}

func TestGroupCertificate(t *testing.T) {
	testGroup := &D{
		DomainResponse: &client.DomainResponse{},
	}

	tls, err := testGroup.EnsureCertificate()
	assert.Nil(t, tls)
	assert.Error(t, err)
}

func TestDomainNoCertificate(t *testing.T) {
	testDomain := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/", Path: "group/project2/public/"},
			},
		},
	}

	tls, err := testDomain.EnsureCertificate()
	assert.Nil(t, tls)
	assert.Error(t, err)

	_, err2 := testDomain.EnsureCertificate()
	assert.Error(t, err)
	assert.Equal(t, err, err2)
}

func TestDomainCertificate(t *testing.T) {
	testDomain := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/", Path: "group/project2/public/"},
			},
			Certificate: fixture.Certificate,
			Key:         fixture.Key,
		},
	}

	tls, err := testDomain.EnsureCertificate()
	assert.NotNil(t, tls)
	require.NoError(t, err)
}

func TestCacheControlHeaders(t *testing.T) {
	cleanup := setUpTests(t)
	defer cleanup()

	testGroup := &D{
		DomainResponse: &client.DomainResponse{
			LookupPath: []client.LookupPath{
				{Prefix: "/", Path: "group/group.test.io/public/"},
			},
		},
	}
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "http://group.test.io/", nil)
	require.NoError(t, err)

	now := time.Now()
	serveFileOrNotFound(testGroup)(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "max-age=600", w.Header().Get("Cache-Control"))

	expires := w.Header().Get("Expires")
	require.NotEmpty(t, expires)

	expiresTime, err := time.Parse(time.RFC1123, expires)
	require.NoError(t, err)

	assert.WithinDuration(t, now.UTC().Add(10*time.Minute), expiresTime.UTC(), time.Minute)
}

// func TestOpenNoFollow(t *testing.T) {
// 	tmpfile, err := ioutil.TempFile("", "link-test")
// 	require.NoError(t, err)
// 	defer tmpfile.Close()

// 	orig := tmpfile.Name()
// 	softLink := orig + ".link"
// 	defer os.Remove(orig)

// 	source, err := openNoFollow(orig)
// 	require.NoError(t, err)
// 	require.NotNil(t, source)
// 	defer source.Close()

// 	err = os.Symlink(orig, softLink)
// 	require.NoError(t, err)
// 	defer os.Remove(softLink)

// 	link, err := openNoFollow(softLink)
// 	require.Error(t, err)
// 	require.Nil(t, link)
// }

var chdirSet = false

func setUpTests(t require.TestingT) func() {
	return chdirInPath(t, "../../shared/pages")
}

func chdirInPath(t require.TestingT, path string) func() {
	noOp := func() {}
	if chdirSet {
		return noOp
	}

	cwd, err := os.Getwd()
	require.NoError(t, err, "Cannot Getwd")

	err = os.Chdir(path)
	require.NoError(t, err, "Cannot Chdir")

	chdirSet = true
	return func() {
		err := os.Chdir(cwd)
		require.NoError(t, err, "Cannot Chdir in cleanup")

		chdirSet = false
	}
}

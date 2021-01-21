package testhelpers

import (
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// AssertHTTP404 asserts handler returns 404 with provided str body
func AssertHTTP404(t *testing.T, handler http.HandlerFunc, mode, url string, values url.Values, str interface{}) {
	w := httptest.NewRecorder()
	req, err := http.NewRequest(mode, url+"?"+values.Encode(), nil)
	require.NoError(t, err)
	handler(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "HTTP status")

	if str != nil {
		contentType, _, _ := mime.ParseMediaType(w.Header().Get("Content-Type"))
		require.Equal(t, "text/html", contentType, "Content-Type")
		require.Contains(t, w.Body.String(), str)
	}
}

// AssertRedirectTo asserts that handler redirects to particular URL
func AssertRedirectTo(t *testing.T, handler http.HandlerFunc, method string,
	url string, values url.Values, expectedURL string) {
	require.HTTPRedirect(t, handler, method, url, values)

	recorder := httptest.NewRecorder()

	req, _ := http.NewRequest(method, url, nil)
	req.URL.RawQuery = values.Encode()

	handler(recorder, req)

	require.Equal(t, expectedURL, recorder.Header().Get("Location"))
}

// AssertLogContains checks that wantLogEntry is contained in at least one of the log entries
func AssertLogContains(t *testing.T, wantLogEntry string, entries []*logrus.Entry) {
	t.Helper()

	if wantLogEntry != "" {
		messages := make([]string, len(entries))
		for k, entry := range entries {
			messages[k] = entry.Message
		}

		require.Contains(t, messages, wantLogEntry)
	}
}

// ToFileProtocol appends the file:// protocol to the current os.Getwd
// and formats path to be a full filepath
func ToFileProtocol(t *testing.T, path string) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	return fmt.Sprintf("file://%s/%s", wd, path)
}

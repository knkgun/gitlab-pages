package acceptance_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-pages/internal/feature"
)

func TestRewrites(t *testing.T) {
	RunPagesProcess(t,
		withListeners([]ListenSpec{httpListener}),
		withEnv([]string{feature.RedirectsPlaceholders.EnvVariable + "=true"}),
	)

	tests := map[string]struct {
		host         string
		path         string
		expectedBody string
	}{
		"rewrite_for_splat_with_replacement": {
			host:         "group.redirects.gitlab-example.com",
			path:         "/project-redirects/arrakis/visitors-guide.html",
			expectedBody: "Welcome to Dune!",
		},
		"splat_with_no_replacement": {
			host:         "group.redirects.gitlab-example.com",
			path:         "/project-redirects/spa/client/side/route",
			expectedBody: "This is an SPA",
		},
		"existing_content_takes_precedence_over_rewrite_rules": {
			host:         "group.redirects.gitlab-example.com",
			path:         "/project-redirects/spa/existing-file.html",
			expectedBody: "This is an existing file",
		},
		"rewrite_using_placeholders": {
			host:         "group.redirects.gitlab-example.com",
			path:         "/project-redirects/blog/2021/08/12",
			expectedBody: "Rewrites are pretty neat!",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rsp, err := GetPageFromListener(t, httpListener, tt.host, tt.path)
			require.NoError(t, err)
			defer rsp.Body.Close()

			body, err := io.ReadAll(rsp.Body)
			require.NoError(t, err)

			require.Contains(t, string(body), tt.expectedBody)
			require.Equal(t, http.StatusOK, rsp.StatusCode)
		})
	}
}

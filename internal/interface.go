package internal

import (
	"net/http"
)

// Artifact allows to handle artifact related requests
type Artifact interface {
	TryMakeRequest(host string, w http.ResponseWriter, r *http.Request, token string, responseHandler func(*http.Response) bool) bool
}

// Auth handles the authentication logic
type Auth interface {
	AuthorizationMiddleware(handler http.Handler) http.Handler
	IsAuthSupported() bool
	RequireAuth(w http.ResponseWriter, r *http.Request) bool
	GetTokenIfExists(w http.ResponseWriter, r *http.Request) (string, error)
	CheckResponseForInvalidToken(w http.ResponseWriter, r *http.Request, resp *http.Response) bool
	CheckAuthenticationWithoutProject(w http.ResponseWriter, r *http.Request, domain domain) bool
}

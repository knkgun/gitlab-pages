package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/namsral/flag"
	log "github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitlab-pages/internal/tlsconfig"
)

// VERSION stores the information about the semantic version of application
var VERSION = "dev"

// REVISION stores the information about the git revision of application
var REVISION = "HEAD"

func init() {
	flag.Var(&listenHTTP, "listen-http", "The address(es) to listen on for HTTP requests")
	flag.Var(&listenHTTPS, "listen-https", "The address(es) to listen on for HTTPS requests")
	flag.Var(&listenProxy, "listen-proxy", "The address(es) to listen on for proxy requests")
}

var (
	pagesRootCert          = flag.String("root-cert", "", "The default path to file certificate to serve static pages")
	pagesRootKey           = flag.String("root-key", "", "The default path to file certificate to serve static pages")
	redirectHTTP           = flag.Bool("redirect-http", false, "Redirect pages from HTTP to HTTPS")
	useHTTP2               = flag.Bool("use-http2", true, "Enable HTTP2 support")
	pagesRoot              = flag.String("pages-root", "shared/pages", "The directory where pages are stored")
	pagesDomain            = flag.String("pages-domain", "gitlab-example.com", "The domain to serve static pages")
	artifactsServer        = flag.String("artifacts-server", "", "API URL to proxy artifact requests to, e.g.: 'https://gitlab.com/api/v4'")
	artifactsServerTimeout = flag.Int("artifacts-server-timeout", 10, "Timeout (in seconds) for a proxied request to the artifacts server")
	pagesStatus            = flag.String("pages-status", "", "The url path for a status page, e.g., /@status")
	metricsAddress         = flag.String("metrics-address", "", "The address to listen on for metrics requests")
	daemonUID              = flag.Uint("daemon-uid", 0, "Drop privileges to this user")
	daemonGID              = flag.Uint("daemon-gid", 0, "Drop privileges to this group")
	daemonInplaceChroot    = flag.Bool("daemon-inplace-chroot", false, "Fall back to a non-bind-mount chroot of -pages-root when daemonizing")
	logFormat              = flag.String("log-format", "text", "The log output format: 'text' or 'json'")
	logVerbose             = flag.Bool("log-verbose", false, "Verbose logging")
	adminSecretPath        = flag.String("admin-secret-path", "", "Path to the file containing the admin secret token")
	adminUnixListener      = flag.String("admin-unix-listener", "", "The path for the admin API unix socket listener (optional)")
	adminHTTPSListener     = flag.String("admin-https-listener", "", "The listen address for the admin API HTTPS listener (optional)")
	adminHTTPSCert         = flag.String("admin-https-cert", "", "The path to the certificate file for the admin API (optional)")
	adminHTTPSKey          = flag.String("admin-https-key", "", "The path to the key file for the admin API (optional)")
	secret                 = flag.String("auth-secret", "", "Cookie store hash key, should be at least 32 bytes long.")
	gitLabServer           = flag.String("auth-server", "", "GitLab server, for example https://www.gitlab.com")
	clientID               = flag.String("auth-client-id", "", "GitLab application Client ID")
	clientSecret           = flag.String("auth-client-secret", "", "GitLab application Client Secret")
	redirectURI            = flag.String("auth-redirect-uri", "", "GitLab application redirect URI")
	maxConns               = flag.Uint("max-conns", 5000, "Limit on the number of concurrent connections to the HTTP, HTTPS or proxy listeners")
	insecureCiphers        = flag.Bool("insecure-ciphers", false, "Use default list of cipher suites, may contain insecure ones like 3DES and RC4")
	tlsMinVersion          = flag.String("tls-min-version", "tls1.2", tlsconfig.FlagUsage("min"))
	tlsMaxVersion          = flag.String("tls-max-version", "", tlsconfig.FlagUsage("max"))

	disableCrossOriginRequests = flag.Bool("disable-cross-origin-requests", false, "Disable cross-origin requests")

	// See init()
	listenHTTP  MultiStringFlag
	listenHTTPS MultiStringFlag
	listenProxy MultiStringFlag
)

var (
	errArtifactSchemaUnsupported   = errors.New("artifacts-server scheme must be either http:// or https://")
	errArtifactsServerTimeoutValue = errors.New("artifacts-server-timeout must be greater than or equal to 1")

	errSecretNotDefined       = errors.New("auth-secret must be defined if authentication is supported")
	errClientIDNotDefined     = errors.New("auth-client-id must be defined if authentication is supported")
	errClientSecretNotDefined = errors.New("auth-client-secret must be defined if authentication is supported")
	errGitLabServerNotDefined = errors.New("auth-server must be defined if authentication is supported")
	errRedirectURINotDefined  = errors.New("auth-redirect-uri must be defined if authentication is supported")
)

func configFromFlags() appConfig {
	var config appConfig

	config.Domain = strings.ToLower(*pagesDomain)
	config.RedirectHTTP = *redirectHTTP
	config.HTTP2 = *useHTTP2
	config.DisableCrossOriginRequests = *disableCrossOriginRequests
	config.StatusPath = *pagesStatus
	config.LogFormat = *logFormat
	config.LogVerbose = *logVerbose
	config.MaxConns = int(*maxConns)
	config.InsecureCiphers = *insecureCiphers
	// tlsMinVersion and tlsMaxVersion are validated in appMain
	config.TLSMinVersion = tlsconfig.AllTLSVersions[*tlsMinVersion]
	config.TLSMaxVersion = tlsconfig.AllTLSVersions[*tlsMaxVersion]

	for _, file := range []struct {
		contents *[]byte
		path     string
	}{
		{&config.RootCertificate, *pagesRootCert},
		{&config.RootKey, *pagesRootKey},
		{&config.AdminCertificate, *adminHTTPSCert},
		{&config.AdminKey, *adminHTTPSKey},
		{&config.AdminToken, *adminSecretPath},
	} {
		if file.path != "" {
			*file.contents = readFile(file.path)
		}
	}

	if *artifactsServerTimeout < 1 {
		log.Fatal(errArtifactsServerTimeoutValue)
	}

	if *artifactsServer != "" {
		u, err := url.Parse(*artifactsServer)
		if err != nil {
			log.Fatal(err)
		}
		// url.Parse ensures that the Scheme arttribute is always lower case.
		if u.Scheme != "http" && u.Scheme != "https" {
			log.Fatal(errArtifactSchemaUnsupported)
		}

		if *artifactsServerTimeout < 1 {
			log.Fatal(errArtifactsServerTimeoutValue)
		}

		config.ArtifactsServerTimeout = *artifactsServerTimeout
		config.ArtifactsServer = *artifactsServer
	}

	checkAuthenticationConfig(config)

	config.StoreSecret = *secret
	config.ClientID = *clientID
	config.ClientSecret = *clientSecret
	config.GitLabServer = *gitLabServer
	config.RedirectURI = *redirectURI

	return config
}

func checkAuthenticationConfig(config appConfig) {
	if *secret != "" || *clientID != "" || *clientSecret != "" ||
		*gitLabServer != "" || *redirectURI != "" {
		// Check all auth params are valid
		assertAuthConfig()
	}
}

func assertAuthConfig() {
	if *secret == "" {
		log.Fatal(errSecretNotDefined)
	}
	if *clientID == "" {
		log.Fatal(errClientIDNotDefined)
	}
	if *clientSecret == "" {
		log.Fatal(errClientSecretNotDefined)
	}
	if *gitLabServer == "" {
		log.Fatal(errGitLabServerNotDefined)
	}
	if *redirectURI == "" {
		log.Fatal(errRedirectURINotDefined)
	}
}

func appMain() {
	var showVersion = flag.Bool("version", false, "Show version")

	flag.String(flag.DefaultConfigFlagname, "", "path to config file")
	flag.Parse()
	if err := tlsconfig.ValidateTLSVersions(*tlsMinVersion, *tlsMaxVersion); err != nil {
		fatal(err)
	}

	printVersion(*showVersion, VERSION)

	configureLogging(*logFormat, *logVerbose)

	log.WithFields(log.Fields{
		"version":  VERSION,
		"revision": REVISION,
	}).Print("GitLab Pages Daemon")
	log.Printf("URL: https://gitlab.com/gitlab-org/gitlab-pages")

	if err := os.Chdir(*pagesRoot); err != nil {
		fatal(err)
	}

	config := configFromFlags()

	log.WithFields(log.Fields{
		"admin-https-cert":              *adminHTTPSCert,
		"admin-https-key":               *adminHTTPSKey,
		"admin-https-listener":          *adminHTTPSListener,
		"admin-unix-listener":           *adminUnixListener,
		"admin-secret-path":             *adminSecretPath,
		"artifacts-server":              *artifactsServer,
		"artifacts-server-timeout":      *artifactsServerTimeout,
		"daemon-gid":                    *daemonGID,
		"daemon-uid":                    *daemonUID,
		"daemon-inplace-chroot":         *daemonInplaceChroot,
		"default-config-filename":       flag.DefaultConfigFlagname,
		"disable-cross-origin-requests": *disableCrossOriginRequests,
		"domain":                        config.Domain,
		"insecure-ciphers":              config.InsecureCiphers,
		"listen-http":                   strings.Join(listenHTTP, ","),
		"listen-https":                  strings.Join(listenHTTPS, ","),
		"listen-proxy":                  strings.Join(listenProxy, ","),
		"log-format":                    *logFormat,
		"metrics-address":               *metricsAddress,
		"pages-domain":                  *pagesDomain,
		"pages-root":                    *pagesRoot,
		"pages-status":                  *pagesStatus,
		"redirect-http":                 config.RedirectHTTP,
		"root-cert":                     *pagesRootKey,
		"root-key":                      *pagesRootCert,
		"status_path":                   config.StatusPath,
		"tls-min-version":               *tlsMinVersion,
		"tls-max-version":               *tlsMaxVersion,
		"use-http-2":                    config.HTTP2,
		"auth-secret":                   config.StoreSecret,
		"auth-server":                   config.GitLabServer,
		"auth-client-id":                config.ClientID,
		"auth-client-secret":            config.ClientSecret,
		"auth-redirect-uri":             config.RedirectURI,
	}).Debug("Start daemon with configuration")

	for _, cs := range [][]io.Closer{
		createAppListeners(&config),
		createMetricsListener(&config),
		createAdminUnixListener(&config),
		createAdminHTTPSListener(&config),
	} {
		defer closeAll(cs)
	}

	if *daemonUID != 0 || *daemonGID != 0 {
		if err := daemonize(config, *daemonUID, *daemonGID, *daemonInplaceChroot); err != nil {
			fatal(err)
		}

		return
	}

	runApp(config)
}

func closeAll(cs []io.Closer) {
	for _, c := range cs {
		c.Close()
	}
}

// createAppListeners returns net.Listener and *os.File instances. The
// caller must ensure they don't get closed or garbage-collected (which
// implies closing) too soon.
func createAppListeners(config *appConfig) []io.Closer {
	var closers []io.Closer

	for _, addr := range listenHTTP.Split() {
		l, f := createSocket(addr)
		closers = append(closers, l, f)

		log.WithFields(log.Fields{
			"listener": addr,
		}).Debug("Set up HTTP listener")

		config.ListenHTTP = append(config.ListenHTTP, f.Fd())
	}

	for _, addr := range listenHTTPS.Split() {
		l, f := createSocket(addr)
		closers = append(closers, l, f)

		log.WithFields(log.Fields{
			"listener": addr,
		}).Debug("Set up HTTPS listener")

		config.ListenHTTPS = append(config.ListenHTTPS, f.Fd())
	}

	for _, addr := range listenProxy.Split() {
		l, f := createSocket(addr)
		closers = append(closers, l, f)

		log.WithFields(log.Fields{
			"listener": addr,
		}).Debug("Set up proxy listener")

		config.ListenProxy = append(config.ListenProxy, f.Fd())
	}

	return closers
}

// createMetricsListener returns net.Listener and *os.File instances. The
// caller must ensure they don't get closed or garbage-collected (which
// implies closing) too soon.
func createMetricsListener(config *appConfig) []io.Closer {
	addr := *metricsAddress
	if addr == "" {
		return nil
	}

	l, f := createSocket(addr)
	config.ListenMetrics = f.Fd()

	log.WithFields(log.Fields{
		"listener": addr,
	}).Debug("Set up metrics listener")

	return []io.Closer{l, f}
}

// createAdminUnixListener returns net.Listener and *os.File instances. The
// caller must ensure they don't get closed or garbage-collected (which
// implies closing) too soon.
func createAdminUnixListener(config *appConfig) []io.Closer {
	unixPath := *adminUnixListener
	if unixPath == "" {
		return nil
	}

	if *adminSecretPath == "" {
		fatal(fmt.Errorf("missing admin secret token file"))
	}

	l, f := createUnixSocket(unixPath)
	config.ListenAdminUnix = f.Fd()

	log.WithFields(log.Fields{
		"listener": unixPath,
	}).Debug("Set up admin unix socket")

	return []io.Closer{l, f}
}

// createAdminHTTPSListener returns net.Listener and *os.File instances. The
// caller must ensure they don't get closed or garbage-collected (which
// implies closing) too soon.
func createAdminHTTPSListener(config *appConfig) []io.Closer {
	addr := *adminHTTPSListener
	if addr == "" {
		return nil
	}

	if *adminSecretPath == "" {
		fatal(fmt.Errorf("missing admin secret token file"))
	}

	l, f := createSocket(addr)
	config.ListenAdminHTTPS = f.Fd()

	log.WithFields(log.Fields{
		"listener": addr,
	}).Debug("Set up admin HTTPS socket")

	return []io.Closer{l, f}
}

func printVersion(showVersion bool, version string) {
	if showVersion {
		fmt.Fprintf(os.Stdout, "%s\n", version)
		os.Exit(0)
	}
}

func main() {
	log.SetOutput(os.Stderr)

	daemonMain()
	appMain()
}

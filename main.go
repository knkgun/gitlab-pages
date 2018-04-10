package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/namsral/flag"

	log "github.com/sirupsen/logrus"
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
	logFormat              = flag.String("log-format", "text", "The log output format: 'text' or 'json'")
	logVerbose             = flag.Bool("log-verbose", false, "Verbose logging")

	disableCrossOriginRequests = flag.Bool("disable-cross-origin-requests", false, "Disable cross-origin requests")

	// See init()
	listenHTTP  MultiStringFlag
	listenHTTPS MultiStringFlag
	listenProxy MultiStringFlag
)

var (
	errArtifactSchemaUnsupported   = errors.New("artifacts-server scheme must be either http:// or https://")
	errArtifactsServerTimeoutValue = errors.New("artifacts-server-timeout must be greater than or equal to 1")
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

	if *pagesRootCert != "" {
		config.RootCertificate = readFile(*pagesRootCert)
	}

	if *pagesRootKey != "" {
		config.RootKey = readFile(*pagesRootKey)
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
	return config
}

func appMain() {
	var showVersion = flag.Bool("version", false, "Show version")

	flag.String(flag.DefaultConfigFlagname, "", "path to config file")
	flag.Parse()

	printVersion(*showVersion, VERSION)

	configureLogging(*logFormat, *logVerbose)

	log.WithFields(log.Fields{
		"version":  VERSION,
		"revision": REVISION,
	}).Print("GitLab Pages Daemon")
	log.Printf("URL: https://gitlab.com/gitlab-org/gitlab-pages")

	err := os.Chdir(*pagesRoot)
	if err != nil {
		fatal(err)
	}

	config := configFromFlags()

	log.WithFields(log.Fields{
		"artifacts-server":              *artifactsServer,
		"artifacts-server-timeout":      *artifactsServerTimeout,
		"daemon-gid":                    *daemonGID,
		"daemon-uid":                    *daemonUID,
		"default-config-filename":       flag.DefaultConfigFlagname,
		"disable-cross-origin-requests": *disableCrossOriginRequests,
		"domain":                        config.Domain,
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
		"use-http-2":                    config.HTTP2,
	}).Debug("Start daemon with configuration")

	for _, addr := range listenHTTP.Split() {
		l, fd := createSocket(addr)
		defer l.Close()

		log.WithFields(log.Fields{
			"listener": addr,
		}).Debug("Set up HTTP listener")

		config.ListenHTTP = append(config.ListenHTTP, fd)
	}

	for _, addr := range listenHTTPS.Split() {
		l, fd := createSocket(addr)
		defer l.Close()

		log.WithFields(log.Fields{
			"listener": addr,
		}).Debug("Set up HTTPS listener")

		config.ListenHTTPS = append(config.ListenHTTPS, fd)
	}

	for _, addr := range listenProxy.Split() {
		l, fd := createSocket(addr)
		defer l.Close()

		log.WithFields(log.Fields{
			"listener": addr,
		}).Debug("Set up proxy listener")

		config.ListenProxy = append(config.ListenProxy, fd)
	}

	if *metricsAddress != "" {
		l, fd := createSocket(*metricsAddress)
		defer l.Close()

		log.WithFields(log.Fields{
			"listener": *metricsAddress,
		}).Debug("Set up metrics listener")

		config.ListenMetrics = fd
	}

	if *daemonUID != 0 || *daemonGID != 0 {
		daemonize(config, *daemonUID, *daemonGID)
		return
	}

	runApp(config)
}

func printVersion(showVersion bool, version string) {
	if showVersion {
		fmt.Fprintf(os.Stderr, version)
		os.Exit(0)
	}
}

func main() {
	log.SetOutput(os.Stderr)

	daemonMain()
	appMain()
}

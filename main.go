package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
)

var VERSION = "dev"
var REVISION = "HEAD"

var listenHTTP = flag.String("listen-http", ":80", "The address to listen for HTTP requests")
var listenHTTPS = flag.String("listen-https", "", "The address to listen for HTTPS requests")
var listenProxy = flag.String("listen-proxy", "", "The address to listen for proxy requests")
var pagesDomain = flag.String("pages-domain", "gitlab-example.com", "The domain to serve static pages")
var pagesRootCert = flag.String("root-cert", "", "The default path to file certificate to serve static pages")
var pagesRootKey = flag.String("root-key", "", "The default path to file certificate to serve static pages")
var serverHTTP = flag.Bool("serve-http", true, "Serve the pages under HTTP")
var http2proto = flag.Bool("http2", true, "Enable HTTP2 support")
var pagesRoot = flag.String("pages-root", "shared/pages", "The directory where pages are stored")

const XForwardedProto = "X-Forwarded-Proto"
const XForwardedProtoHttps = "https"

type theApp struct {
	domains domains
}

func (a *theApp) ServeTLS(ch *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if ch.ServerName == "" {
		return nil, nil
	}

	host := strings.ToLower(ch.ServerName)
	if domain, ok := a.domains[host]; ok {
		tls, _ := domain.ensureCertificate()
		return tls, nil
	}

	return nil, nil
}

func (a *theApp) serveContent(ww http.ResponseWriter, r *http.Request, https bool) {
	w := newLoggingResponseWriter(ww)
	defer w.Log(r)

	// Add auto redirect
	if https && !*serverHTTP {
		u := *r.URL
		u.Scheme = "https"
		u.Host = r.Host
		u.User = nil

		http.Redirect(&w, r, u.String(), 307)
		return
	}

	host := strings.ToLower(r.Host)
	domain, ok := a.domains[host]

	if !ok {
		http.NotFound(&w, r)
		return
	}

	// Serve static file
	domain.ServeHTTP(&w, r)
}

func (a *theApp) ServeHTTP(ww http.ResponseWriter, r *http.Request) {
	https := r.TLS != nil
	a.serveContent(ww, r, https)
}

func (a *theApp) ServeProxy(ww http.ResponseWriter, r *http.Request) {
	forwardedProto := r.Header.Get(XForwardedProto)
	https := forwardedProto == XForwardedProtoHttps
	a.serveContent(ww, r, https)
}

func (a *theApp) UpdateDomains(domains domains) {
	fmt.Printf("Domains: %v", domains)
	a.domains = domains
}

func main() {
	var wg sync.WaitGroup
	var app theApp

	fmt.Println("GitLab Pages Daemon %s (%s)", VERSION, REVISION)
	fmt.Println("URL: https://gitlab.com/gitlab-org/gitlab-pages")

	flag.Parse()

	// Listen for HTTP
	if *listenHTTP != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := ListenAndServe(*listenHTTP, app.ServeHTTP)
			if err != nil {
				log.Fatal(err)
			}
		}()
	}

	// Listen for HTTPS
	if *listenHTTPS != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := ListenAndServeTLS(*listenHTTPS, *pagesRootCert, *pagesRootKey, app.ServeHTTP, app.ServeTLS)
			if err != nil {
				log.Fatal(err)
			}
		}()
	}

	// Listen for HTTP proxy requests
	if *listenProxy != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := ListenAndServe(*listenProxy, app.ServeProxy)
			if err != nil {
				log.Fatal(err)
			}
		}()
	}

	go watchDomains(app.UpdateDomains)

	wg.Wait()
}

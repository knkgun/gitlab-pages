package httptransport

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	sysPoolOnce = &sync.Once{}
	sysPool     *x509.CertPool

	// only overridden by transport_darwin.go
	loadExtraCerts = func() {}
	// InternalTransport can be used with http.Client with TLS and certificates
	InternalTransport = newInternalTransport()
)

type meteredRoundTripper struct {
	next      http.RoundTripper
	durations *prometheus.GaugeVec
	counter   *prometheus.CounterVec
}

func newInternalTransport() *http.Transport {
	return &http.Transport{
		DialTLS: func(network, addr string) (net.Conn, error) {
			return tls.Dial(network, addr, &tls.Config{RootCAs: pool()})
		},
		Proxy: http.ProxyFromEnvironment,
		// overrides the DefaultMaxIdleConnsPerHost = 2
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}
}

// NewTransportWithMetrics will create a custom http.RoundTripper that can be used with an http.Client.
// The RoundTripper will report metrics based on the collectors passed.
func NewTransportWithMetrics(gaugeVec *prometheus.GaugeVec, counterVec *prometheus.CounterVec) http.RoundTripper {
	return &meteredRoundTripper{
		next:      InternalTransport,
		durations: gaugeVec,
		counter:   counterVec,
	}
}

// This is here because macOS does not support the SSL_CERT_FILE and
// SSL_CERT_DIR environment variables. We have arranged things to read
// SSL_CERT_FILE and SSL_CERT_DIR  as late as possible to avoid conflicts
// with file descriptor passing at startup.
func pool() *x509.CertPool {
	sysPoolOnce.Do(loadPool)
	return sysPool
}

func loadPool() {
	var err error

	// Always load the system cert pool
	sysPool, err = x509.SystemCertPool()
	if err != nil {
		log.WithError(err).Error("failed to load system cert pool for http client")
		return
	}

	// Go does not load SSL_CERT_FILE and SSL_CERT_DIR on darwin systems so we need to
	// load them manually in OSX. See https://golang.org/src/crypto/x509/root_unix.go
	loadExtraCerts()
}

// withRoundTripper takes an original RoundTripper, reports metrics based on the
// gauge and counter collectors passed
func (mrt *meteredRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()

	resp, err := mrt.next.RoundTrip(r)
	if err != nil {
		mrt.counter.WithLabelValues("error").Inc()
		return nil, err
	}

	statusCode := strconv.Itoa(resp.StatusCode)
	mrt.durations.WithLabelValues(statusCode).Set(time.Since(start).Seconds())
	mrt.counter.WithLabelValues(statusCode).Inc()

	return resp, nil
}

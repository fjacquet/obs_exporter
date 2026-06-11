// Command mockecs is a minimal fake Dell ECS management API for end-to-end demos.
// It serves the same REST surface the exporter calls (basic-auth GET /login issuing
// an X-SDS-AUTH-TOKEN, the dashboard/namespace GETs, and the bulk billing POST)
// over self-signed TLS on :4443, returning canned JSON from embedded fixtures. It
// is NOT a faithful ECS emulator — it exists so the Compose stack lights up a
// Grafana dashboard without real hardware.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"io"
	"log"
	"math/big"
	"net/http"
	"time"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// getRoutes maps an exporter GET path to its embedded fixture file.
var getRoutes = map[string]string{
	"/dashboard/zones/localzone":                   "fixtures/localzone.json",
	"/dashboard/zones/localzone/replicationgroups": "fixtures/replicationgroups.json",
	"/dashboard/zones/localzone/nodes":             "fixtures/nodes.json",
	"/vdc/nodes":                                   "fixtures/vdc-nodes.json",
	"/object/namespaces":                           "fixtures/namespaces.json",
	"/object/namespaces/namespace/s3/quota":        "fixtures/quota-s3.json",
	"/object/namespaces/namespace/swift/quota":     "fixtures/quota-swift.json",
}

const mockToken = "mockecs-session-token"

func main() {
	mux := http.NewServeMux()

	// Auth: a basic-auth GET issues the session token header; /logout releases it.
	// Credentials are not checked — this is a demo appliance.
	mux.HandleFunc("/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-SDS-AUTH-TOKEN", mockToken)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for path, file := range getRoutes {
		mux.HandleFunc(path, fixtureHandler(http.MethodGet, file))
	}
	// Bulk namespace billing (OBS 4.1) is a POST; the request body is ignored and
	// the canned response covers every demo namespace.
	mux.HandleFunc("/object/billing/namespace/info", fixtureHandler(http.MethodPost, "fixtures/billing.json"))

	srv := &http.Server{
		Addr:              ":4443",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{mustSelfSignedCert()},
		},
	}
	log.Println("mockecs: serving fake ECS management API on https://localhost:4443")
	log.Fatal(srv.ListenAndServeTLS("", ""))
}

// fixtureHandler returns a handler that requires the session token and serves the
// named embedded fixture as JSON.
func fixtureHandler(method, file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-SDS-AUTH-TOKEN") != mockToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, err := fixtures.ReadFile(file)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, b)
	}
}

// writeBytes writes b to w. It takes io.Writer (not http.ResponseWriter) so the raw
// write is isolated to one helper, the same pattern the tests use.
func writeBytes(w io.Writer, b []byte) { _, _ = w.Write(b) }

// mustSelfSignedCert generates an in-memory self-signed certificate at startup.
// Clients connect with insecureSkipVerify, so the cert only needs to be valid TLS.
func mustSelfSignedCert() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("mockecs: generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mockecs"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * 365 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"mockecs", "localhost"},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		log.Fatalf("mockecs: create cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

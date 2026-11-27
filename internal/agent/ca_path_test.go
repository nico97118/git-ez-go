// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestConfiguredCASymlinkParentUsesPhysicalTrust(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Unrelated test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	unrelated, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"openai", "ollama"} {
		for _, trusted := range []bool{false, true} {
			for _, absolute := range []bool{false, true} {
				t.Run(provider+"/"+map[bool]string{false: "untrusted", true: "trusted"}[trusted]+"/"+map[bool]string{false: "relative", true: "absolute"}[absolute], func(t *testing.T) {
					_, g, _, close := setup(t, provider)
					close()
					f := &modelFixture{steps: []step{read("file.txt"), write("file.txt", "combined\n"), finish(.95)}}
					server := httptest.NewTLSServer(f)
					defer server.Close()
					physical := t.TempDir()
					if err := os.Mkdir(physical+"/child", 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(physical+"/child", g.Dir+"/link"); err != nil {
						t.Fatal(err)
					}
					// An ignored local configuration resource must not make Git admission dirty.
					testutil.Must(t, g, "config", "core.excludesFile", "/dev/null")
					if err := os.WriteFile(g.Dir+"/.git/info/exclude", []byte("/link\n/ca.pem\n"), 0600); err != nil {
						t.Fatal(err)
					}
					intended, wrong := server.TLS.Certificates[0].Certificate[0], unrelated
					if !trusted {
						intended, wrong = wrong, intended
					}
					for path, raw := range map[string][]byte{physical + "/ca.pem": intended, g.Dir + "/ca.pem": wrong} {
						if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600); err != nil {
							t.Fatal(err)
						}
					}
					path := "link/../ca.pem"
					if absolute {
						path = g.Dir + "/" + path
					}
					endpoint := server.URL + "/v1/chat/completions"
					if provider == "ollama" {
						endpoint = server.URL + "/api/chat"
					}
					for k, v := range map[string]string{"ez.model": "fixture", "ez.provider": provider, "ez.endpoint": endpoint, "ez.caBundle": path} {
						testutil.Must(t, g, "config", k, v)
					}
					c, err := config.Load(context.Background(), g)
					canonical, _ := filepath.EvalSymlinks(physical + "/ca.pem")
					if err != nil || c.CABundle == nil || *c.CABundle != canonical {
						t.Fatal(c.CABundle, err, canonical)
					}
					r, err := runEngine(t, c, g)
					if trusted && (err != nil || !r.Integrated || len(f.requests) != 3) {
						t.Fatal(r, err, len(f.requests))
					}
					if !trusted && (err == nil || r.Integrated || len(f.requests) != 0) {
						t.Fatal("unrelated CA established trust", r, err, len(f.requests))
					}
				})
			}
		}
	}
}

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/chironjit/serversend/apps/server/internal/localtls"
	"github.com/chironjit/serversend/shared/client"

	"github.com/chironjit/serversend/shared/protocol"
)

func TestVerifiedIdentityGatesEveryConnectionAndAllowsLeafRenewal(t *testing.T) {
	for _, dual := range []bool{false, true} {
		t.Run(map[bool]string{false: "http2", true: "http3"}[dual], func(t *testing.T) {
			cfg := fixture(t)
			cfg.TLSCertFile = ""
			cfg.TLSKeyFile = ""
			cfg.TLSIPs = "127.0.0.1"
			s, err := New(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			authority, err := os.ReadFile(filepath.Join(cfg.ConfigDir, localtls.Directory, localtls.CAFile))
			if err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []string{"private text\n" + string(authority), string(authority) + "private text", string(authority) + string(authority), ""} {
				if _, err := client.AuthorityFingerprint(invalid); err == nil {
					t.Fatal("CA import accepted extra material")
				}
			}
			initial, err := s.tls.GetCertificate(&tls.ClientHelloInfo{})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			selected := initial
			s.tls = httpapi.BindIdentityCertificate(&tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				mu.Lock()
				defer mu.Unlock()
				return selected, nil
			}})
			var credentials atomic.Int32
			var quicProofs atomic.Int32
			apiHandler := httpapi.New(s.ready, httpapi.WithIdentity(s.signIdentity), httpapi.WithAuthentication(s.accounts))
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/identity/proof" && r.ProtoMajor == 3 {
					quicProofs.Add(1)
				}
				if r.URL.Path == "/api/v1/auth/password" {
					credentials.Add(1)
				}
				apiHandler.ServeHTTP(w, r)
			})
			var origin string
			if dual {
				origin = startDualServer(t, s, handler)
			} else {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- s.serve(ctx, listener, handler) }()
				t.Cleanup(func() {
					cancel()
					if err := <-done; err != nil {
						t.Error(err)
					}
				})
				origin = "https://" + listener.Addr().String()
			}
			untrusted, _ := client.NewVerified(origin, client.Trust{})
			if err = untrusted.VerifyIdentity(t.Context()); err == nil {
				t.Fatal("private CA accepted without provisioning")
			}
			untrusted.Close()
			paired, err := client.NewVerified(origin, client.Trust{AuthorityPEM: string(authority)})
			if err != nil {
				t.Fatal(err)
			}
			if err = paired.VerifyIdentity(t.Context()); err != nil {
				t.Fatal("pairing", err)
			}
			trust, err := paired.VerifiedTrust()
			if err != nil || trust.Identity.ServerID != protocol.ID(s.Identity().ID) {
				t.Fatal("identity pin", err)
			}
			// Complete mandatory onboarding through the production verified client.
			issued, err := s.accounts.BootstrapAdmin(t.Context(), "user")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE accounts SET role='user' WHERE username='user'`); err != nil {
				t.Fatal(err)
			}
			paired.SetInstallationID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			onboardWithClient(t, paired, "user", issued.TemporaryPassword)
			var durable client.CredentialState
			paired.SetCredentialStore(func(state client.CredentialState) error { durable = state; return nil })
			saved := paired.Credentials()
			paired.Close()
			renewed := renewIdentityFixtureLeaf(t, cfg.ConfigDir)
			mu.Lock()
			selected = renewed
			mu.Unlock()
			restored, err := client.NewVerified(origin, trust)
			if err != nil {
				t.Fatal(err)
			}
			if err = restored.VerifyIdentity(t.Context()); err != nil {
				t.Fatal("renewed leaf rejected", err)
			}
			restored.SetCredentialStore(func(state client.CredentialState) error { durable = state; return nil })
			if err = restored.RestoreCredentials(t.Context(), saved); err != nil {
				t.Fatal("session not restored after renewal", err)
			}
			if durable.Pending != nil || durable.Session.DeviceID != saved.Session.DeviceID {
				t.Fatal("renewal changed device or lost rotation")
			}
			devices, err := restored.Devices(t.Context(), "")
			if err != nil || len(devices) != 1 || !devices[0].Current {
				t.Fatal("self-service devices", err)
			}
			// A transport disconnect keeps credentials; the replacement verifies again.
			restored.Close()
			credentials.Store(0)
			if _, err = s.db.Exec(`UPDATE server_identity SET key_seed=? WHERE singleton=1`, make([]byte, 32)); err != nil {
				t.Fatal(err)
			}
			replacement, err := client.NewVerified(origin, trust)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			if _, err = replacement.Password(t.Context(), "user", "must-never-be-sent"); !errors.Is(err, client.ErrIdentityChanged) {
				t.Fatal("identity replacement accepted", err)
			}
			if dual && quicProofs.Load() < 2 {
				t.Fatal("verified pairing/renewal failed to use HTTP/3")
			}
			if credentials.Load() != 0 {
				t.Fatal("credentials released before identity verification")
			}
		})
	}
}
func renewIdentityFixtureLeaf(t *testing.T, dir string) *tls.Certificate {
	t.Helper()
	privateCA, err := os.ReadFile(filepath.Join(dir, localtls.Directory, "authority.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := tls.X509KeyPair(privateCA, privateCA)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(42), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, key.Public(), ca.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	if err != nil {
		t.Fatal(err)
	}
	return &renewed
}

func TestVerifiedRefreshAndInstallationSurviveServerRestart(t *testing.T) {
	cfg := fixture(t)
	cfg.TLSCertFile = ""
	cfg.TLSKeyFile = ""
	cfg.TLSIPs = "127.0.0.1"
	start := func(address string) (*Server, string, func()) {
		t.Helper()
		s, err := New(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- s.Serve(ctx, listener) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		t.Cleanup(stop)
		return s, listener.Addr().String(), stop
	}
	first, address, stop := start("127.0.0.1:0")
	authority, err := os.ReadFile(filepath.Join(cfg.ConfigDir, localtls.Directory, localtls.CAFile))
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewVerified("https://"+address, client.Trust{AuthorityPEM: string(authority)})
	if err != nil {
		t.Fatal(err)
	}
	c.SetInstallationID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	issued, err := first.accounts.BootstrapAdmin(t.Context(), "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.db.Exec(`UPDATE accounts SET role='user' WHERE username='user'`); err != nil {
		t.Fatal(err)
	}
	c, password, codes := onboardWithRecovery(t, c, "user", issued.TemporaryPassword)
	trust, err := c.VerifiedTrust()
	if err != nil {
		t.Fatal(err)
	}
	saved := c.Credentials()
	c.Close()
	stop()
	start(address)
	restored, err := client.NewVerified("https://"+address, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.SetInstallationID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err = restored.RestoreCredentials(t.Context(), saved); err != nil {
		t.Fatal("refresh after server restart", err)
	}
	current, _ := restored.Account()
	if current.DeviceID != saved.Session.DeviceID {
		t.Fatal("restart changed device")
	}
	// An explicit full login on the same installation must replace its grants
	// while retaining one device entry, even after server persistence/restart.
	attempt, err := restored.Password(t.Context(), "user", password)
	if err != nil {
		t.Fatal(err)
	}
	next, err := restored.SignIn(t.Context(), attempt.Challenge, codes[1], "Renamed laptop")
	if err != nil {
		t.Fatal(err)
	}
	if next.DeviceID != current.DeviceID {
		t.Fatal("full login after restart duplicated installation")
	}
	devices, err := restored.Devices(t.Context(), "")
	if err != nil || len(devices) != 1 || devices[0].Name != "Renamed laptop" {
		t.Fatal("device history after restart", devices, err)
	}
	old, err := client.NewVerified("https://"+address, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err = old.RestoreCredentials(t.Context(), saved); err == nil {
		t.Fatal("full login left old refresh authority valid")
	}
}
